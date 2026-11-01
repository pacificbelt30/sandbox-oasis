package agentbackup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
)

func TestPayloadRequiresDigestLengthAndEOF(t *testing.T) {
	data := bytes.Repeat([]byte{3}, 1<<16)
	sum := sha256.Sum256(data)
	h := Header{Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	for _, tc := range []struct {
		name string
		data []byte
		good bool
	}{
		{"complete", data, true}, {"truncated", data[:len(data)-1], false}, {"appended", append(append([]byte{}, data...), 0), false}, {"corrupt", bytes.Repeat([]byte{4}, len(data)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CopyPayload(io.Discard, bytes.NewReader(tc.data), h)
			if (err == nil) != tc.good {
				t.Fatal(err)
			}
		})
	}
}
func TestHeaderRoundtripAndBounds(t *testing.T) {
	h := Header{Format: "oaiso-agent-state-v1", Bytes: 64 << 20, UID: 1000, GID: 1000, SHA256: string(bytes.Repeat([]byte{'a'}, 64))}
	var b bytes.Buffer
	if err := WriteHeader(&b, h); err != nil {
		t.Fatal(err)
	}
	out, err := ReadHeader(&b)
	if err != nil || out != h {
		t.Fatal(out, err)
	}
	for _, data := range [][]byte{[]byte("not-a-backup"), []byte("OAISODB1\xff\xff\xff\xff"), []byte("OAISODB1\x00\x00\x00\x01{")} {
		if _, err = ReadHeader(bytes.NewReader(data)); err == nil {
			t.Fatal("invalid header accepted")
		}
	}
}
