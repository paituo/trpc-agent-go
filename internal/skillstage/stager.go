//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skillstage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fsutil"
)

const (
	skillDirInputs = "inputs"
	skillDirVenv   = ".venv"
)

const workspaceMetadataFileMode uint32 = 0o600

// Stager materializes skill package contents into a workspace and maintains
// the corresponding workspace metadata and links.
type Stager struct{}

// New creates a skill stager.
func New() *Stager {
	return &Stager{}
}

// StageOptions tunes how StageSkillWithOptions materializes a skill
// working copy. The zero value matches the default behavior expected
// by the workspaceprep reconciler: a writable session-level working
// copy that scripts may freely modify.
type StageOptions struct {
	// ReadOnly flips the staged tree to read-only after copy. This
	// is the legacy behavior used by the now-deprecated skill_run
	// tool and should not be used by new callers; treating skills/
	// as a writable working copy is the default contract.
	ReadOnly bool
}

// StageSkill copies a skill into the shared workspace and links the shared
// work/out roots under skills/<name>. The staged tree is writable by
// default; callers that need the legacy read-only semantics can use
// StageSkillWithOptions.
func (s *Stager) StageSkill(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	root string,
	name string,
) error {
	return s.StageSkillWithOptions(ctx, eng, ws, root, name, StageOptions{})
}

// StageSkillWithOptions is StageSkill with explicit knobs. It exists
// so legacy entry points can request the old read-only behavior while
// new workspace-preparation code keeps the writable-by-default
// contract.
func (s *Stager) StageSkillWithOptions(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	root string,
	name string,
	opts StageOptions,
) error {
	dg, err := codeexecutor.DirDigest(root)
	if err != nil {
		return err
	}
	return codeexecutor.WithWorkspaceMetadataLock(
		ctx,
		ws.Path,
		func(ctx context.Context) error {
			return s.stageSkillWithOptionsLocked(
				ctx, eng, ws, root, name, dg, opts,
			)
		},
	)
}

func (s *Stager) stageSkillWithOptionsLocked(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	root string,
	name string,
	dg string,
	opts StageOptions,
) error {
	md, err := s.LoadWorkspaceMetadata(ctx, eng, ws)
	if err != nil {
		return err
	}
	dest := path.Join(codeexecutor.DirSkills, name)
	if meta, ok := md.Skills[name]; ok &&
		meta.Digest == dg && meta.Mounted {
		ok, err := s.SkillLinksPresent(ctx, eng, ws, name)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	if err := s.RemoveWorkspacePath(ctx, eng, ws, dest); err != nil {
		return err
	}
	if err := eng.FS().StageDirectory(
		ctx,
		ws,
		root,
		dest,
		codeexecutor.StageOptions{ReadOnly: false, AllowMount: false},
	); err != nil {
		return err
	}
	if err := s.linkWorkspaceDirs(ctx, eng, ws, name); err != nil {
		return err
	}
	if opts.ReadOnly {
		if err := s.readOnlyExceptSymlinks(
			ctx, eng, ws, dest,
		); err != nil {
			return err
		}
	}
	md.Skills[name] = codeexecutor.SkillMeta{
		Name:     name,
		RelPath:  dest,
		Digest:   dg,
		Mounted:  true,
		StagedAt: time.Now(),
	}
	return s.SaveWorkspaceMetadata(ctx, eng, ws, md)
}

// LoadWorkspaceMetadata reads workspace metadata from the shared metadata file
// and returns a normalized in-memory view with defaults applied.
func (s *Stager) LoadWorkspaceMetadata(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
) (codeexecutor.WorkspaceMetadata, error) {
	now := time.Now()
	md := codeexecutor.NewWorkspaceMetadata()
	if eng == nil || eng.FS() == nil {
		return md, fmt.Errorf("workspace fs is not configured")
	}
	files, err := eng.FS().Collect(
		ctx, ws, []string{codeexecutor.MetaFileName},
	)
	if err != nil {
		return md, err
	}
	if len(files) == 0 || strings.TrimSpace(files[0].Content) == "" {
		return md, nil
	}
	if err := json.Unmarshal([]byte(files[0].Content), &md); err != nil {
		if codeexecutor.IsMetadataCorruptError(err) {
			return codeexecutor.NewWorkspaceMetadata(), nil
		}
		return codeexecutor.WorkspaceMetadata{}, err
	}
	if md.Version == 0 {
		md.Version = 1
	}
	if md.CreatedAt.IsZero() {
		md.CreatedAt = now
	}
	md.LastAccess = now
	if md.Skills == nil {
		md.Skills = map[string]codeexecutor.SkillMeta{}
	}
	return md, nil
}

// SaveWorkspaceMetadata persists workspace metadata into the shared metadata
// file within the current workspace.
func (s *Stager) SaveWorkspaceMetadata(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	md codeexecutor.WorkspaceMetadata,
) error {
	if eng == nil || eng.FS() == nil {
		return fmt.Errorf("workspace fs is not configured")
	}
	if md.Version == 0 {
		md.Version = 1
	}
	now := time.Now()
	if md.CreatedAt.IsZero() {
		md.CreatedAt = now
	}
	md.UpdatedAt = now
	md.LastAccess = now
	if md.Skills == nil {
		md.Skills = map[string]codeexecutor.SkillMeta{}
	}
	buf, err := json.MarshalIndent(md, "", "  ")
	if err != nil {
		return err
	}
	tmpFile := codeexecutor.MetadataTempFileName()
	tmpPath := filepath.Join(ws.Path, filepath.FromSlash(tmpFile))
	destPath := filepath.Join(
		ws.Path, filepath.FromSlash(codeexecutor.MetaFileName),
	)
	committed := false
	defer cleanupMetadataTempFile(tmpPath, &committed)
	if err := eng.FS().PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    tmpFile,
		Content: buf,
		Mode:    workspaceMetadataFileMode,
	}}); err != nil {
		return err
	}
	// Reject if destination path is a directory.
	if fi, err := os.Stat(destPath); err == nil && fi.IsDir() {
		return fmt.Errorf("metadata path is a directory")
	}
	_ = os.Remove(destPath)
	if err := os.Rename(tmpPath, destPath); err != nil {
		return err
	}
	committed = true
	return nil
}

// cleanupMetadataTempFile removes the temporary metadata file unless
// the commit flag has been set to true.
func cleanupMetadataTempFile(tmpPath string, committed *bool) {
	if committed != nil && *committed {
		return
	}
	if tmpPath == "" {
		return
	}
	_ = os.Remove(tmpPath)
}

// SkillLinksPresent reports whether the staged skill directory still exposes
// the expected shared-directory symlinks back into the workspace roots.
func (s *Stager) SkillLinksPresent(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	name string,
) (bool, error) {
	skillName := strings.TrimSpace(name)
	if skillName == "" {
		return false, nil
	}
	base := filepath.Join(
		ws.Path,
		filepath.FromSlash(path.Join(codeexecutor.DirSkills, skillName)),
	)
	for _, linkName := range []string{
		codeexecutor.DirOut,
		codeexecutor.DirWork,
		skillDirInputs,
	} {
		p := filepath.Join(base, linkName)
		ok, err := fsutil.IsLink(p)
		if err != nil || !ok {
			return false, nil
		}
	}
	return true, nil
}

// linkWorkspaceDirs creates symlinks from a staged skill directory back to
// the shared workspace out/, work/, and work/inputs/ directories.
// On Windows without symlink privileges it falls back to directory junctions
// via the fsutil package.
func (s *Stager) linkWorkspaceDirs(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	name string,
) error {
	skillRel := filepath.FromSlash(
		path.Join(codeexecutor.DirSkills, name),
	)
	skillRoot := filepath.Join(ws.Path, skillRel)

	// Remove old entries (links or directories).
	for _, d := range []string{
		codeexecutor.DirOut,
		codeexecutor.DirWork,
		skillDirInputs,
		skillDirVenv,
	} {
		_ = os.RemoveAll(filepath.Join(skillRoot, d))
	}

	// Ensure workspace-level work/inputs exists.
	inputsDir := filepath.Join(
		ws.Path,
		filepath.FromSlash(codeexecutor.DirWork),
		skillDirInputs,
	)
	if err := os.MkdirAll(inputsDir, 0o755); err != nil {
		return err
	}

	// Ensure skill-level .venv exists.
	venvDir := filepath.Join(skillRoot, skillDirVenv)
	if err := os.MkdirAll(venvDir, 0o755); err != nil {
		return err
	}

	// Create symlinks back to shared workspace dirs.
	outTarget := filepath.Join(ws.Path, codeexecutor.DirOut)
	workTarget := filepath.Join(ws.Path, codeexecutor.DirWork)
	inputsTarget := inputsDir

	links := []struct {
		target string
		link   string
	}{
		{outTarget, filepath.Join(skillRoot, codeexecutor.DirOut)},
		{workTarget, filepath.Join(skillRoot, codeexecutor.DirWork)},
		{inputsTarget, filepath.Join(skillRoot, skillDirInputs)},
	}
	for _, l := range links {
		if err := fsutil.CreateSymlink(l.target, l.link); err != nil {
			return fmt.Errorf(
				"link workspace dirs: symlink %s -> %s: %w",
				l.link, l.target, err,
			)
		}
	}
	return nil
}

// RemoveWorkspacePath removes a workspace-relative path after first making
// non-symlink files writable so cleanup can succeed on read-only staged trees.
func (s *Stager) RemoveWorkspacePath(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	rel string,
) error {
	target := strings.TrimSpace(rel)
	if target == "" {
		return nil
	}
	absTarget := filepath.Join(ws.Path, filepath.FromSlash(target))

	// Make non-symlink entries writable before removal so we can
	// delete read-only trees (e.g. from legacy ReadOnly staging).
	_ = filepath.WalkDir(absTarget, func(
		p string, d os.DirEntry, err error,
	) error {
		if err != nil {
			return nil
		}
		// Skip symlinks — chmod on a symlink would affect its target.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		_ = os.Chmod(p, info.Mode().Perm()|0o200) // add owner-write.
		return nil
	})
	return os.RemoveAll(absTarget)
}

// readOnlyExceptSymlinks removes write bits from all regular files in dest,
// skipping symlinks and the .venv directory.
func (s *Stager) readOnlyExceptSymlinks(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	dest string,
) error {
	absDest := filepath.Join(ws.Path, filepath.FromSlash(dest))
	venvAbs := filepath.Join(absDest, skillDirVenv)
	return filepath.WalkDir(absDest, func(
		p string, d os.DirEntry, err error,
	) error {
		if err != nil {
			return err
		}
		// Skip .venv directory entirely.
		if p == venvAbs ||
			strings.HasPrefix(p, venvAbs+string(os.PathSeparator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		return os.Chmod(p, info.Mode().Perm()&^0o222)
	})
}
