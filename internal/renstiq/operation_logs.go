package renstiq

import (
	"fmt"
	"os"
	"path/filepath"
)

type FileLogs struct{ Dir string }
type fileOperationLog struct{ *os.File }

func (l fileOperationLog) Path() string { return l.Name() }

func (s FileLogs) Open(runID, operationID string) (OperationLog, error) {
	if runID == "" || filepath.Base(runID) != runID || runID == "." || runID == ".." {
		return nil, fmt.Errorf("invalid run ID for log: %q", runID)
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, runID+"-"+digest(operationID)+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return fileOperationLog{f}, nil
}

func fileLogs(stateDir, repo string) FileLogs {
	if stateDir == "" {
		stateDir = stateHome()
	}
	return FileLogs{Dir: filepath.Join(stateDir, digest(repo)+"-logs")}
}
