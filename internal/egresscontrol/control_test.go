package egresscontrol

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestControlFrameDoesNotWaitForStdinEOF(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		value, err := ReadUpdate(reader)
		if err == nil && value.ExpectedGeneration != 7 {
			t.Error("generation lost")
		}
		done <- err
	}()
	if _, err := io.WriteString(writer, "{\"expected_generation\":7,\"rules\":{\"BlockPrivate\":true}}\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("control helper still waits for EOF")
	}
	for _, line := range []string{"{}", "{} {}\n", "{\"rules\":{},\"rules\":{}}\n", strings.Repeat("x", 64<<10+1) + "\n"} {
		if _, err := ReadUpdate(strings.NewReader(line)); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
}
