// buildgate-challenge: AC-1
package main

import (
	"bufio"
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

func readVersionChallengeRedisCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("invalid redis command header %q", line)
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || count < 1 || count > 100 {
		return nil, fmt.Errorf("invalid redis argument count %q", line)
	}
	arguments := make([]string, 0, count)
	for argument := 0; argument < count; argument++ {
		line, err = reader.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "$") {
			return nil, fmt.Errorf("invalid redis argument header %q: %v", line, err)
		}
		length, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || length < 0 || length > 65536 {
			return nil, fmt.Errorf("invalid redis argument length %q", line)
		}
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return nil, err
		}
		if string(value[length:]) != "\r\n" {
			return nil, fmt.Errorf("invalid redis argument termination")
		}
		arguments = append(arguments, string(value[:length]))
	}
	return arguments, nil
}

func serveVersionChallengeRedisStartup(listener net.Listener) {
	defer listener.Close()
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		command, err := readVersionChallengeRedisCommand(reader)
		if err != nil {
			return
		}
		switch strings.ToUpper(command[0]) {
		case "HELLO":
			_, err = io.WriteString(conn, "-ERR unknown command 'HELLO'\r\n")
		case "CLIENT":
			_, err = io.WriteString(conn, "+OK\r\n")
		case "PING":
			io.WriteString(conn, "+PONG\r\n")
			return
		default:
			_, err = io.WriteString(conn, "-ERR unsupported command\r\n")
		}
		if err != nil {
			return
		}
	}
}

func TestVersionProcessRootChallenge(testContext *testing.T) {
	if os.Getenv("BG_VERSION_CHALLENGE_CHILD") == "1" {
		main()
		return
	}

	redisListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		testContext.Fatal(err)
	}
	testContext.Cleanup(func() { redisListener.Close() })
	go serveVersionChallengeRedisStartup(redisListener)

	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		testContext.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	portListener.Close()

	logPath := filepath.Join(testContext.TempDir(), "api.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		testContext.Fatal(err)
	}
	testContext.Cleanup(func() { logFile.Close() })
	command := exec.Command(os.Args[0], "-test.run=^TestVersionProcessRootChallenge$")
	command.Env = append(os.Environ(),
		"BG_VERSION_CHALLENGE_CHILD=1",
		"REDIS_ADDR="+redisListener.Addr().String(),
		"KAFKA_BROKERS=",
		"TODO_DATABASE_URL=",
		"PORT="+strconv.Itoa(port),
	)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		testContext.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { command.Wait(); close(exited) }()
	testContext.Cleanup(func() {
		command.Process.Kill()
		<-exited
		if testContext.Failed() {
			logBytes, err := os.ReadFile(logPath)
			if err == nil {
				testContext.Logf("API process output: %s", logBytes)
			}
		}
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			testContext.Fatal("API process exited before listening")
		default:
		}
		response, err := client.Get(baseURL + "/healthz")
		if err == nil {
			response.Body.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		testContext.Fatal("API process did not start listening")
	}

	response, err := client.Get(baseURL + "/version")
	if err != nil {
		testContext.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		testContext.Fatal(err)
	}
	var document map[string]any
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &document) != nil || len(document) != 1 || document["service"] != "todo-kafka-service" {
		testContext.Fatalf("GET /version on API process = %d, %s", response.StatusCode, body)
	}
}
