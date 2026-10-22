package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const (
	replaceMaxEntries = 4096
	replaceMaxDepth   = 32
)

var errReplaceTree = errors.New("replacement tree refused")

type replaceEntry struct {
	name string
	info os.FileInfo
}

type replaceTree struct {
	root          *os.Root
	dir           string
	info          os.FileInfo
	entries       []replaceEntry // children precede their directories
	closeResource func(func() error, *error)
}

// The production caller has already verified the dedicated writable kernel
// mount, same-device child and absence of nested mounts. os.Root confines
// removal but does NOT itself prohibit mount traversal. Neither that verifier
// nor this scan claims atomicity against later privileged mount/path changes.
// Stopped writers and the existing trusted-root model remain prerequisites.
func openReplaceTree(ctx context.Context, dir string) (*replaceTree, error) {
	p, owned := &replaceProgress{stage: "replacement_admission"}, false
	return openReplaceTreeUsing(ctx, dir, func(close func() error, result *error) {
		closeReplaceResource(close, p, nil, result, &owned)
	})
}

func openReplaceTreeUsing(ctx context.Context, dir string, closeResource func(func() error, *error)) (tree *replaceTree, result error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errReplaceTree
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			closeResource(root.Close, &result)
		}
	}()
	opened, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, errReplaceTree
	}
	tree = &replaceTree{root: root, dir: dir, info: info, closeResource: closeResource}
	remaining := replaceMaxEntries
	if err := tree.scan(ctx, ".", 0, &remaining); err != nil {
		return nil, err
	}
	found := 0
	for _, entry := range tree.entries {
		switch entry.name {
		case "data.db", "auxiliary.db":
			if !entry.info.Mode().IsRegular() || entry.info.Size() == 0 {
				return nil, errReplaceTree
			}
			found++
		case "data.db-wal", "data.db-shm", "data.db-journal", "auxiliary.db-wal", "auxiliary.db-shm", "auxiliary.db-journal":
			// Immutable read-only admission must not ignore an uncheckpointed
			// commit or create/update shared memory. Require clean stopped files;
			// never checkpoint, repair or remove sidecars to gain admission.
			return nil, errReplaceTree
		}
	}
	if found != 2 {
		return nil, errReplaceTree
	}
	accepted = true
	return tree, nil
}

func (tree *replaceTree) scan(ctx context.Context, dir string, depth int, remaining *int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if depth > replaceMaxDepth {
		return errReplaceTree
	}
	entries, err := tree.readDirectory(dir, *remaining+1)
	if err != nil {
		return err
	}
	if len(entries) > *remaining {
		return errReplaceTree
	}
	*remaining -= len(entries)
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := filepath.Join(dir, entry.Name())
		info, err := tree.root.Lstat(name)
		if err != nil {
			return err
		}
		if !(info.IsDir() || info.Mode().IsRegular()) || !replaceSameDevice(tree.info, info) {
			return errReplaceTree
		}
		if info.IsDir() {
			if err := tree.scan(ctx, name, depth+1, remaining); err != nil {
				return err
			}
		}
		tree.entries = append(tree.entries, replaceEntry{name: name, info: info})
	}
	return nil
}

func (tree *replaceTree) readDirectory(dir string, limit int) (entries []os.DirEntry, result error) {
	f, err := tree.root.Open(dir)
	if err != nil {
		return nil, err
	}
	defer tree.closeResource(f.Close, &result)
	entries, result = f.ReadDir(limit)
	if errors.Is(result, io.EOF) {
		result = nil
	}
	return entries, result
}

func replaceSameDevice(parent, child os.FileInfo) bool {
	p, ok := parent.Sys().(*syscall.Stat_t)
	c, childOK := child.Sys().(*syscall.Stat_t)
	return ok && childOK && p.Dev == c.Dev && (child.IsDir() || c.Nlink == 1)
}

func discardReplaceTree(ctx context.Context, tree *replaceTree, progress *replaceProgress) error {
	return discardReplaceTreeUsing(ctx, tree, progress, tree.root.Remove)
}

func discardReplaceTreeUsing(ctx context.Context, tree *replaceTree, progress *replaceProgress, remove func(string) error) error {
	// Recheck the entire bounded inventory before the first unlink, then each
	// identity at removal. The root directory itself remains for normal Init's
	// empty-directory admission; mount root, lost+found and siblings are untouched.
	current, err := os.Lstat(tree.dir)
	if err != nil || !os.SameFile(tree.info, current) || !current.IsDir() {
		return errReplaceTree
	}
	refreshed := &replaceTree{root: tree.root, info: tree.info, closeResource: tree.closeResource}
	remaining := replaceMaxEntries
	if err := refreshed.scan(ctx, ".", 0, &remaining); err != nil {
		return err
	}
	if len(refreshed.entries) != len(tree.entries) {
		return errReplaceTree
	}
	for _, entry := range tree.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := tree.root.Lstat(entry.name)
		if err != nil || !os.SameFile(entry.info, current) || current.Mode() != entry.info.Mode() || current.Size() != entry.info.Size() {
			return errReplaceTree
		}
	}
	// Remove the admission-bearing data file FIRST. Once it is gone, a partial
	// discard cannot be mistaken for an admissible old ledger by another run.
	ordered := make([]replaceEntry, 0, len(tree.entries))
	for _, entry := range tree.entries {
		if entry.name == "data.db" {
			ordered = append(ordered, entry)
		}
	}
	for _, entry := range tree.entries {
		if entry.name != "data.db" {
			ordered = append(ordered, entry)
		}
	}
	for _, entry := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := tree.root.Lstat(entry.name)
		if err != nil || !os.SameFile(entry.info, current) || current.Mode() != entry.info.Mode() {
			return errReplaceTree
		}
		progress.started = true // before syscall: an error never proves no unlink
		if err := remove(entry.name); err != nil {
			return err
		}
	}
	// Confirm emptiness even if cancellation arrived after the last unlink.
	// This passive check preserves a completed discard; it starts no init.
	entries, err := tree.readDirectory(".", 1)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errReplaceTree
	}
	return nil
}
