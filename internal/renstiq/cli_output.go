package renstiq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type Result struct {
	Version   int          `json:"version"`
	Command   string       `json:"command"`
	Init      *InitResult  `json:"init,omitempty"`
	Results   []RepoResult `json:"results"`
	Discovery []Discovery  `json:"discovery,omitempty"`
	Error     string       `json:"error,omitempty"`
}

func emitResult(out, log io.Writer, result Result, err error) int {
	result.Version = 1
	if result.Results == nil {
		result.Results = []RepoResult{}
	}
	code := 0
	if err != nil {
		result.Error = err.Error()
		code = 1
		var input *InputError
		if errors.As(err, &input) {
			code = 2
		}
	} else {
		for _, r := range result.Results {
			if r.Error != "" {
				code = 1
			}
		}
		for _, d := range result.Discovery {
			if discoveryFailed(d) {
				code = 1
			}
		}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintln(log, err)
		return 1
	}
	return code
}

func jsonAction(name string, run func(context.Context, io.Reader) (Result, error)) cliAction {
	return func(ctx context.Context, in io.Reader, out, log io.Writer) int {
		result, err := run(ctx, in)
		result.Command = name
		return emitResult(out, log, result, err)
	}
}

func batchOutput(batch BatchResult) Result {
	return Result{Results: batch.Results, Discovery: batch.Discovery}
}
func singleOutput(result RepoResult) Result { return Result{Results: []RepoResult{result}} }

func writeText(out, log io.Writer, text string) int {
	if _, err := io.WriteString(out, text); err != nil {
		fmt.Fprintln(log, err)
		return 1
	}
	return 0
}
