package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunStartsWithEmptyDatabaseAndStopsOnCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	content := fmt.Sprintf(`[server]
host="127.0.0.1"
port=%d
[database]
path=%q
[log]
console=false
file=%q
`, port, filepath.Join(dir, "data", "test.db"), filepath.Join(dir, "logs", "test.log"))
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	if err := run(ctx, []string{"-config", configPath}); err != nil {
		t.Fatal(err)
	}
}

func TestRunValidateRejectsAnyInvalidCSVRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "points.csv")
	content := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n" +
		"valid,HoldingReg,0,UInt16,0,16,0,valid point\n" +
		"invalid,HoldingReg,1,UInt16,0,16,yes,invalid point\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-validate", "-csv", path}); err == nil {
		t.Fatal("run() error = nil, want strict CSV validation error")
	}
}
