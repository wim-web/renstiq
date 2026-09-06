package renstiq

import (
	"context"
	"errors"
)

// PRState is the small remote state used for reconciliation, not a REST payload.
type PRState struct {
	Number                                                 int
	Author                                                 string
	Merged                                                 bool
	HeadSHA, HeadBranch, HeadRepo, BaseBranch, MergeCommit string
	Labels                                                 []string
}

type Revision struct{ Head, Base string }

type PRReader interface {
	WaitPR(context.Context, string, int, Revision, bool) (PullRequest, error)
}

type PRStateReader interface {
	PullRequestState(context.Context, string, int) (PRState, error)
}

// RejectedWrite distinguishes a definitive rejection from an uncertain write.
// Transport status codes are classified by the adapter, never by a use case.
type RejectedWrite struct{ Cause error }

func (e *RejectedWrite) Error() string { return e.Cause.Error() }
func (e *RejectedWrite) Unwrap() error { return e.Cause }
func rejectedWrite(err error) bool {
	var rejection *RejectedWrite
	return errors.As(err, &rejection)
}

// These ports are grouped only at the composition root; consumers receive the
// individual role they use, rather than a Get/Write transport or giant interface.
type remotePorts struct {
	Inspect  InspectionReader
	Reader   PRReader
	Comments CommentGateway
	Labels   LabelGateway
	Merge    MergeGateway
	Branches BranchGateway
}
