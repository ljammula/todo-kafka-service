// buildgate-challenge: AC-1
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestChallengeVersionRegisteredByMain(t *testing.T) {
	if os.Getenv("VERSION_CHALLENGE_CHILD") == "1" {
		os.Setenv("REDIS_ADDR", os.Getenv("VERSION_CHALLENGE_REDIS"))
		os.Setenv("PORT", os.Getenv("VERSION_CHALLENGE_PORT"))
		os.Unsetenv("TODO_DATABASE_URL")
		os.Unsetenv("KAFKA_BROKERS")
		main()
		return
	}

	redis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer redis.Close()
	go challengeAcceptRedis(redis)
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(portListener.Addr().(*net.TCPAddr).Port)
	portListener.Close()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestChallengeVersionRegisteredByMain$", "-test.timeout=15s")
	command.Env = append(os.Environ(), "VERSION_CHALLENGE_CHILD=1", "VERSION_CHALLENGE_REDIS="+redis.Addr().String(), "VERSION_CHALLENGE_PORT="+port)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = command.Wait()
		close(done)
	}()
	defer func() {
		command.Process.Kill()
		<-done
	}()

	base := "http://127.0.0.1:" + port
	client := &http.Client{
		Timeout:       300 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	challengeWaitForRoot(t, client, base, done, &exitErr, &output)
	request, err := http.NewRequest(http.MethodGet, base+"/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "gateway.example:443"
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET /version: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]string
	if err := json.Unmarshal(body, &document); response.StatusCode != http.StatusOK || err != nil || len(document) != 1 || document["service"] != "todo-kafka-service" {
		t.Fatalf("GET /version on production root: status = %d, body = %q, decode error = %v", response.StatusCode, body, err)
	}
}

func challengeWaitForRoot(t *testing.T, client *http.Client, base string, done <-chan struct{}, exitErr *error, output *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("API process exited: %v; output: %s", *exitErr, output.String())
		default:
		}
		response, err := client.Get(base + "/healthz")
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("existing root healthz status = %d, want 200", response.StatusCode)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("API process did not start within eight seconds")
}

func challengeAcceptRedis(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go challengeServeRedis(connection)
	}
}

func challengeServeRedis(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	for {
		command, err := challengeReadRedisCommand(reader)
		if err != nil {
			return
		}
		reply := "+OK\r\n"
		switch strings.ToUpper(command) {
		case "HELLO":
			reply = "-ERR unknown command HELLO\r\n"
		case "PING":
			reply = "+PONG\r\n"
		}
		if _, err := io.WriteString(connection, reply); err != nil {
			return
		}
	}
}

func challengeReadRedisCommand(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "*") {
		return "", fmt.Errorf("invalid array header: %q: %v", line, err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || count < 1 || count > 32 {
		return "", fmt.Errorf("invalid argument count: %q", line)
	}
	var command string
	for index := 0; index < count; index++ {
		line, err = reader.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "$") {
			return "", fmt.Errorf("invalid bulk header: %q: %v", line, err)
		}
		length, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || length < 0 || length > 65536 {
			return "", fmt.Errorf("invalid bulk length: %q", line)
		}
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", err
		}
		if index == 0 {
			command = string(value[:length])
		}
	}
	return command, nil
}
