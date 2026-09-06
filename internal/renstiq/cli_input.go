package renstiq

import (
	"errors"
	"io"
	"os"
)

func readDecisionSource(path string, in io.Reader) (Decision, error) {
	if path == "-" {
		if in == nil {
			return Decision{}, errors.New("decision stdin is unavailable")
		}
		return ReadDecision(in)
	}
	f, err := os.Open(path)
	if err != nil {
		return Decision{}, err
	}
	defer f.Close()
	return ReadDecision(f)
}
