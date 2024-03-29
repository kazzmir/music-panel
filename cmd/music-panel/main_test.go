package main

import (
    "testing"
    "os/exec"
    "syscall"
    "path/filepath"
    _ "log"
)

func TestProc(test *testing.T){
    sleep := exec.Command("sleep", "100")
    sleep.Start()

    defer sleep.Process.Signal(syscall.SIGTERM)

    pid := sleep.Process.Pid

    psOutput, err := runPsPid(pid)
    if err != nil {
        test.Fatalf("Unable to get ps output: %v", err)
    }

    // log.Printf("Ps output: %v", psOutput)

    psName, ok := extractPsProcess(psOutput)
    if !ok {
        test.Fatalf("Unable to extract ps command")
    }
    if psName != "sleep" {
        test.Fatalf("Unexpected process name from ps: %v", psName)
    }

    name, err := readProcName(pid)
    if err != nil {
        test.Fatalf("could not read name from /proc: %v", err)
    }

    if name != "sleep" {
        test.Fatalf("unexpected name from proc: %v", name)
    }

    name2, err := readProcExe(pid)
    if err != nil {
        test.Fatalf("could not read proc exe: %v", err)
    }

    if filepath.Base(name2) != "sleep" {
        test.Fatalf("got unexpected exe name: %v", name2)
    }
}

