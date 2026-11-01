// buildgate-challenge: AC-1
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestChallengeVersionIsOnActualRootServer(t *testing.T) {
	if os.Getenv("BUILDGATE_CHALLENGE_RUN_API") == "1" {
		if err := os.Unsetenv("KAFKA_BROKERS"); err != nil {
			t.Fatal(err)
		}
		main()
		return
	}

	redis, stopRedis := challengeStartRedisForAPIStartup(t)
	port := challengeUnusedPort(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestChallengeVersionIsOnActualRootServer$")
	cmd.Env = append(os.Environ(),
		"BUILDGATE_CHALLENGE_RUN_API=1",
		"REDIS_ADDR="+redis,
		"PORT="+port,
		"TODO_DATABASE_URL=",
	)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-exited
	})

	baseURL := "http://127.0.0.1:" + port
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	challengeWaitForRootServer(t, client, baseURL, cmd, exited, &logs)
	stopRedis()
	resp, err := client.Get(baseURL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil || resp.StatusCode != http.StatusOK || len(object) != 1 || object["service"] != "todo-kafka-service" {
		t.Fatalf("GET /version: status %d, body %q, JSON error %v", resp.StatusCode, body, err)
	}
}

func challengeWaitForRootServer(t *testing.T, client *http.Client, baseURL string, cmd *exec.Cmd, exited <-chan struct{}, logs *bytes.Buffer) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("root server health status = %d, want 200", resp.StatusCode)
			}
			return
		}
		select {
		case <-exited:
			t.Fatalf("API exited before listening: %s", logs.String())
		case <-deadline.C:
			cmd.Process.Kill()
			<-exited
			t.Fatalf("API did not listen: %s", logs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func challengeUnusedPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func challengeStartRedisForAPIStartup(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var connections []net.Conn
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections = append(connections, conn)
			go challengeServeStartupRedis(conn)
		}
	}()
	stop := sync.OnceFunc(func() {
		listener.Close()
		<-stopped
		for _, conn := range connections {
			conn.Close()
		}
	})
	t.Cleanup(stop)
	return listener.Addr().String(), stop
}

func challengeServeStartupRedis(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		args, err := challengeReadRedisCommand(reader)
		if err != nil {
			return
		}
		reply := "+OK\r\n"
		switch strings.ToUpper(args[0]) {
		case "HELLO":
			reply = "-ERR unknown command 'HELLO'\r\n"
		case "PING":
			reply = "+PONG\r\n"
		}
		if _, err := io.WriteString(conn, reply); err != nil {
			return
		}
	}
}

func challengeReadRedisCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "*") {
		return nil, errors.New("expected RESP array")
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || count < 1 || count > 32 {
		return nil, fmt.Errorf("invalid RESP array size %q", line)
	}
	args := make([]string, count)
	for index := range args {
		args[index], err = challengeReadRedisBulkString(reader)
		if err != nil {
			return nil, err
		}
	}
	return args, nil
}

func challengeReadRedisBulkString(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(line, "$") {
		return "", errors.New("expected RESP bulk string")
	}
	length, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || length < 0 || length > 16384 {
		return "", fmt.Errorf("invalid RESP bulk string length %q", line)
	}
	data := make([]byte, length+2)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", err
	}
	if string(data[length:]) != "\r\n" {
		return "", errors.New("invalid RESP bulk string terminator")
	}
	return string(data[:length]), nil
}
