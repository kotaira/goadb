package adb

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/kotaira/goadb/internal/errors"
	"github.com/kotaira/goadb/wire"
)

type shellPipeServer struct{ conn net.Conn }

func (s shellPipeServer) Start() error { return nil }
func (s shellPipeServer) Dial() (*wire.Conn, error) {
	safe := wire.MultiCloseable(s.conn)
	return wire.NewConn(wire.NewScanner(safe), wire.NewSender(safe)), nil
}

func TestOpenShellStreamsMultipleCommands(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() { client.Close(); peer.Close() })
	client.SetDeadline(time.Now().Add(3 * time.Second))
	peer.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		done <- func() error {
			scanner := wire.NewScanner(peer)
			for _, want := range []string{"host:transport:serial", "shell:"} {
				payload, err := scanner.ReadMessage()
				if err != nil {
					return err
				}
				if string(payload) != want {
					return fmt.Errorf("request %q, want %q", payload, want)
				}
				if _, err := io.WriteString(peer, "OKAY"); err != nil {
					return err
				}
			}
			reader := bufio.NewReader(peer)
			for i, want := range []string{"cd /data/local/tmp\n", "cat example.txt\n", "exit\n"} {
				line, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				if line != want {
					return fmt.Errorf("raw input %q, want %q", line, want)
				}
				if _, err := fmt.Fprintf(peer, "output %d\n", i); err != nil {
					return err
				}
			}
			_, err := reader.ReadByte()
			if err != io.EOF {
				return fmt.Errorf("Close should end the stream: %v", err)
			}
			return nil
		}()
	}()
	dev := (&Adb{server: shellPipeServer{client}}).Device(DeviceWithSerial("serial"))
	shell, err := dev.OpenShell()
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(shell.GetReader())
	for i, command := range []string{"cd /data/local/tmp", "cat example.txt", "exit"} {
		if _, err := io.WriteString(shell.GetWriter(), command+"\n"); err != nil {
			t.Fatal(err)
		}
		line, err := reader.ReadString('\n')
		if err != nil || line != fmt.Sprintf("output %d\n", i) {
			t.Fatalf("output %q: %v", line, err)
		}
	}
	if err := shell.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOpenShellClosesFailedHandshake(t *testing.T) {
	failure := errors.Errorf(errors.NetworkError, "disconnected")
	for _, errs := range [][]error{
		{nil, nil, nil, failure},
		{nil, nil, nil, nil, failure},
	} {
		srv := &MockServer{Status: wire.StatusSuccess, Errs: errs}
		dev := (&Adb{server: srv}).Device(AnyDevice())
		shell, err := dev.OpenShell()
		if shell != nil || !HasErrCode(err, NetworkError) {
			t.Fatalf("shell=%v err=%v", shell, err)
		}
		if !reflect.DeepEqual(srv.Trace[len(srv.Trace)-2:], []string{"Close", "Close"}) {
			t.Fatalf("connection leaked: %v", srv.Trace)
		}
	}
}

func TestRunCommandWithShellKeepsCommandAndValidation(t *testing.T) {
	srv := &MockServer{Status: wire.StatusSuccess}
	dev := (&Adb{server: srv}).Device(AnyDevice())
	shell, err := dev.RunCommandWithShell("cat", "file name")
	if err != nil {
		t.Fatal(err)
	}
	defer shell.Close()
	if !reflect.DeepEqual(srv.Requests, []string{"host:transport-any", `shell:cat "file name"`}) {
		t.Fatal(srv.Requests)
	}
	if _, err := dev.RunCommandWithShell(""); err == nil {
		t.Fatal("empty command must still be rejected")
	}
}
