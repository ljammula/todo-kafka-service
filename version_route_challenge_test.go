// buildgate-challenge: AC-1
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func challengeReadRedisCommand(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(line, "*") {
		return "", fmt.Errorf("expected RESP array, got %q", line)
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || count < 1 || count > 100 {
		return "", fmt.Errorf("invalid RESP array length: %q", line)
	}
	var command string
	for index := 0; index < count; index++ {
		line, err = reader.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "$") {
			return "", fmt.Errorf("invalid RESP bulk length %q: %v", line, err)
		}
		length, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || length < 0 || length > 65536 {
			return "", fmt.Errorf("invalid RESP bulk length: %q", line)
		}
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", err
		}
		if index == 0 {
			command = strings.ToUpper(string(value[:length]))
		}
	}
	return command, nil
}

func challengeServeRedisConnection(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	for {
		command, err := challengeReadRedisCommand(reader)
		if err != nil {
			return
		}
		response := "+OK\r\n"
		switch command {
		case "HELLO":
			response = "-ERR unknown command 'HELLO'\r\n"
		case "PING":
			response = "+PONG\r\n"
		}
		if _, err := io.WriteString(connection, response); err != nil {
			return
		}
	}
}

func challengeStartRedisStub(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go challengeServeRedisConnection(connection)
		}
	}()
	return listener.Addr().String()
}

func challengeBuildAPIBinary(t *testing.T) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "todo-api")
	buildContext, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	output, err := exec.CommandContext(buildContext, "go", "build", "-o", executable, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build API: %v\n%s", err, output)
	}
	return executable
}

func challengeStartAPI(t *testing.T, executable, redisAddress string) (string, <-chan struct{}, string) {
	t.Helper()
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(reservation.Addr().String())
	reservation.Close()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "api.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logFile.Close() })
	command := exec.Command(executable)
	command.Env = append(os.Environ(), "PORT="+port, "REDIS_ADDR="+redisAddress, "KAFKA_BROKERS=", "TODO_DATABASE_URL=")
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		t.Fatalf("start API: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		command.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		command.Process.Kill()
		<-exited
	})
	return "http://127.0.0.1:" + port, exited, logPath
}

func challengeWaitForRootAPI(t *testing.T, client *http.Client, baseURL string, exited <-chan struct{}, logPath string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-exited:
			logs, _ := os.ReadFile(logPath)
			t.Fatalf("API exited before serving /healthz: %s", logs)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	logs, _ := os.ReadFile(logPath)
	t.Fatalf("API did not serve /healthz before timeout: %s", logs)
}

func TestChallengeVersionOnRunningRootAPI(t *testing.T) {
	redisAddress := challengeStartRedisStub(t)
	executable := challengeBuildAPIBinary(t)
	baseURL, exited, logPath := challengeStartAPI(t, executable, redisAddress)
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	challengeWaitForRootAPI(t, client, baseURL, exited, logPath)
	response, err := client.Get(baseURL + "/version")
	if err != nil {
		t.Fatalf("GET /version on API process: %v", err)
	}
	defer response.Body.Close()
	var body map[string]any
	decodeErr := json.NewDecoder(response.Body).Decode(&body)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" || decodeErr != nil || len(body) != 1 || body["service"] != "todo-kafka-service" {
		t.Fatalf("GET /version on API process = status %d, Content-Type %q, body %#v, decode error %v", response.StatusCode, response.Header.Get("Content-Type"), body, decodeErr)
	}
}
