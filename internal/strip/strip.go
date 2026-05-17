package strip

import (
	"fmt"
	"os"
	"os/exec"
)

type Result struct {
	StrippedPath string
	DebugPath    string
}

func Strip(inputPath string) (*Result, error) {
	strippedPath := inputPath + ".stripped"
	debugPath := inputPath + ".debug"

	data, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	if err := os.WriteFile(strippedPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("copy for stripping: %w", err)
	}

	if err := exec.Command("objcopy", "--only-keep-debug", strippedPath, debugPath).Run(); err != nil {
		os.Remove(strippedPath)
		return nil, fmt.Errorf("extract debug info: %w", err)
	}

	if err := exec.Command("strip", "--strip-debug", "--strip-unneeded", strippedPath).Run(); err != nil {
		os.Remove(strippedPath)
		os.Remove(debugPath)
		return nil, fmt.Errorf("strip binary: %w", err)
	}

	return &Result{StrippedPath: strippedPath, DebugPath: debugPath}, nil
}

func Available() bool {
	_, err1 := exec.LookPath("strip")
	_, err2 := exec.LookPath("objcopy")
	return err1 == nil && err2 == nil
}
