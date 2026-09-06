package renstiq

import (
	"context"
	"errors"
	"fmt"
)

func synchronize(ctx context.Context, dir, repo string, merges []MergeRecord) error {
	if len(merges) == 0 {
		return errors.New("cannot synchronize without a merged PR")
	}
	// working_dir is the command's directory, not necessarily the checkout root.
	// Keep repository/config discovery strict while synchronizing the whole tree.
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	name, err := repository(ctx, root)
	if err != nil {
		return err
	}
	if name != repo {
		return errors.New("post-merge working directory belongs to another repository")
	}
	return synchronizeCheckout(ctx, root, merges)
}

func synchronizeCheckout(ctx context.Context, dir string, merges []MergeRecord) error {
	branch := merges[len(merges)-1].Base
	for _, m := range merges {
		if m.Base != branch {
			return errors.New("cannot synchronize merges for different base branches in one action")
		}
	}
	status, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("working tree has local changes; synchronization stopped")
	}
	current, err := git(ctx, dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	if current != branch {
		return fmt.Errorf("working directory is on %s; expected %s", current, branch)
	}
	if _, err = git(ctx, dir, "fetch", "--no-tags", "origin", "refs/heads/"+branch); err != nil {
		return err
	}
	target, err := git(ctx, dir, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	if _, err = git(ctx, dir, "merge-base", "--is-ancestor", "HEAD", target); err != nil {
		return errors.New("local branch has diverged or is ahead; refusing synchronization")
	}
	for _, m := range merges {
		if m.Commit == "" {
			return errors.New("merged commit is unknown")
		}
		if _, err = git(ctx, dir, "merge-base", "--is-ancestor", m.Commit, target); err != nil {
			return fmt.Errorf("merged commit %s is not in fetched base", m.Commit)
		}
	}
	if _, err = git(ctx, dir, "merge", "--ff-only", target); err != nil {
		return err
	}
	head, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != target {
		return errors.New("working directory did not reach fetched base")
	}
	status, err = git(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("working tree changed during synchronization")
	}
	return nil
}
