package renstiq

import "time"

// Engine is only the assembled use-case facade. Each service below it consumes
// narrow role-specific ports; no service accesses a GitHub transport or Store.
type Engine struct {
	*FeedbackService
	*MergeService
	*PostMergeService
}

func newEngine(repo string, journal Journal, remote remotePorts, executor *PostExecutor, clock func() time.Time) *Engine {
	reconciler := MergeReconciler{Repo: repo, Remote: remote.Merge, Journal: journal}
	cleaner := BranchCleaner{Repo: repo, Remote: remote.Branches, Journal: journal, Now: clock}
	post := &PostMergeService{Journal: journal, Reconcile: reconciler.Reconcile, Cleanup: cleaner.Cleanup, Actions: executor}
	feedback := &FeedbackService{
		Repo: repo, Reader: remote.Reader, Journal: journal,
		Comments: CommentService{Repo: repo, Remote: remote.Comments, Journal: journal, Now: clock},
		Labels:   LabelService{Repo: repo, Remote: remote.Labels, Journal: journal, Now: clock},
	}
	merge := &MergeService{Repo: repo, Remote: remote.Merge, Journal: journal, Reconciler: reconciler, AfterMerge: post.PostMerge}
	return &Engine{FeedbackService: feedback, MergeService: merge, PostMergeService: post}
}
