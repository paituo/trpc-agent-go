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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/internal/fsutil"
)

type stubFS struct {
	collectFiles []codeexecutor.File
	collectErr   error
	putErr       error
	putCalls     int
	putFiles     []codeexecutor.PutFile
}

func (s *stubFS) PutFiles(
	_ context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	s.putCalls++
	s.putFiles = append(s.putFiles, files...)
	if s.putErr != nil {
		return s.putErr
	}
	// Write files to disk so that subsequent Go-level file
	// operations (e.g. os.Rename) can find them.
	for _, f := range files {
		p := filepath.Join(ws.Path, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, f.Content, os.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	return nil
}

func (*stubFS) StageDirectory(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ string,
	_ string,
	_ codeexecutor.StageOptions,
) error {
	return nil
}

func (s *stubFS) Collect(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []string,
) ([]codeexecutor.File, error) {
	if s.collectErr != nil {
		return nil, s.collectErr
	}
	return s.collectFiles, nil
}

func (*stubFS) StageInputs(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []codeexecutor.InputSpec,
) error {
	return nil
}

func (*stubFS) CollectOutputs(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type stubEngine struct {
	f codeexecutor.WorkspaceFS
}

func (*stubEngine) Manager() codeexecutor.WorkspaceManager { return nil }
func (e *stubEngine) FS() codeexecutor.WorkspaceFS         { return e.f }
func (*stubEngine) Runner() codeexecutor.ProgramRunner     { return nil }
func (*stubEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

func TestStager_LoadSaveMetadata_CoversBranches(t *testing.T) {
	st := New()
	ctx := context.Background()
	ws := codeexecutor.Workspace{ID: "x", Path: t.TempDir()}

	_, err := st.LoadWorkspaceMetadata(ctx, nil, ws)
	require.Error(t, err)

	fs := &stubFS{collectErr: fmt.Errorf("collect fail")}
	eng := &stubEngine{f: fs}
	_, err = st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.Error(t, err)

	fs.collectErr = nil
	md, err := st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)

	fs.collectFiles = []codeexecutor.File{{
		Name:    codeexecutor.MetaFileName,
		Content: " \n\t ",
	}}
	md, err = st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)

	fs.collectFiles = []codeexecutor.File{{
		Name: codeexecutor.MetaFileName,
		Content: `{"version":0,"created_at":"0001-01-01T00:00:00Z",` +
			`"updated_at":"0001-01-01T00:00:00Z","last_access":"0001-01-01T00:00:00Z","skills":null}`,
	}}
	start := time.Now()
	md, err = st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)
	require.False(t, md.CreatedAt.IsZero())
	require.False(t, md.CreatedAt.Before(start))

	fs.collectFiles = []codeexecutor.File{{
		Name:    codeexecutor.MetaFileName,
		Content: "not-json}",
	}}
	md, err = st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)

	// SaveWorkspaceMetadata: nil engine.
	err = st.SaveWorkspaceMetadata(
		ctx, nil, ws, codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)

	// SaveWorkspaceMetadata: no runner is now fine — it uses Go ops.
	fs.putErr = fmt.Errorf("put fail")
	err = st.SaveWorkspaceMetadata(
		ctx, eng, ws, codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)
	require.Equal(t, 1, fs.putCalls)
	const (
		metadataTmpPrefix = ".metadata."
		metadataTmpSuffix = ".tmp"
	)
	require.True(t, strings.HasPrefix(
		fs.putFiles[0].Path,
		metadataTmpPrefix,
	))
	require.True(t, strings.HasSuffix(
		fs.putFiles[0].Path,
		metadataTmpSuffix,
	))
	require.Equal(t, workspaceMetadataFileMode, fs.putFiles[0].Mode)

	// SaveWorkspaceMetadata: successful write + rename.
	fs.putErr = nil
	fs.putCalls = 0
	fs.putFiles = nil
	err = st.SaveWorkspaceMetadata(
		ctx, eng, ws, codeexecutor.WorkspaceMetadata{},
	)
	require.NoError(t, err)
	require.Equal(t, 1, fs.putCalls)

	// Tmp file should be gone (renamed to metadata).
	matches, err := filepath.Glob(
		filepath.Join(ws.Path, ".metadata.*.tmp"),
	)
	require.NoError(t, err)
	require.Empty(t, matches)

	// Metadata file should exist and be valid JSON.
	raw, err := os.ReadFile(
		filepath.Join(ws.Path, codeexecutor.MetaFileName),
	)
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
}

func TestStager_SaveWorkspaceMetadata_MetadataDirectoryFails(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "stage-metadata-dir", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Cleanup(ctx, ws) })

	require.NoError(
		t,
		os.Remove(filepath.Join(ws.Path, codeexecutor.MetaFileName)),
	)
	require.NoError(
		t,
		os.Mkdir(filepath.Join(ws.Path, codeexecutor.MetaFileName), 0o755),
	)

	err = New().SaveWorkspaceMetadata(
		ctx,
		eng,
		ws,
		codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "metadata path is a directory")

	matches, err := filepath.Glob(filepath.Join(ws.Path, ".metadata.*.tmp"))
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestCleanupMetadataTempFile_Nop(t *testing.T) {
	committed := true
	cleanupMetadataTempFile("tmp", &committed)
	cleanupMetadataTempFile("", nil)
	cleanupMetadataTempFile("", &committed)

	// When not committed, cleanup removes the tmp file.
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "metadata.test.tmp")
	require.NoError(t, os.WriteFile(tmpPath, []byte("x"), 0o644))
	notCommitted := false
	cleanupMetadataTempFile(tmpPath, &notCommitted)
	_, err := os.Stat(tmpPath)
	require.True(t, os.IsNotExist(err))
}

func TestStager_StageSkillAndLinks(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "stage-skill", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Cleanup(ctx, ws)
	})

	root := t.TempDir()
	skillRoot := filepath.Join(root, "echoer")
	require.NoError(t, os.MkdirAll(skillRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillRoot, "SKILL.md"),
		[]byte("body"),
		0o644,
	))

	st := New()
	err = st.StageSkill(ctx, eng, ws, skillRoot, "echoer")
	require.NoError(t, err)

	files, err := rt.Collect(ctx, ws, []string{"skills/echoer/SKILL.md"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "body", files[0].Content)

	md, err := st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	meta, ok := md.Skills["echoer"]
	require.True(t, ok)
	require.Equal(t, "echoer", meta.Name)
	require.Equal(t, filepath.ToSlash("skills/echoer"), meta.RelPath)
	require.True(t, meta.Mounted)
	require.NotEmpty(t, meta.Digest)

	ok, err = st.SkillLinksPresent(ctx, eng, ws, "echoer")
	require.NoError(t, err)
	require.True(t, ok)

	checkSymlink := func(rel string) {
		t.Helper()
		ok, err := fsutil.IsLink(filepath.Join(
			ws.Path, filepath.FromSlash(rel),
		))
		require.NoError(t, err)
		require.True(t, ok, "expected link at %s", rel)
	}
	checkSymlink("skills/echoer/out")
	checkSymlink("skills/echoer/work")
	checkSymlink("skills/echoer/inputs")

	fi, err := os.Stat(filepath.Join(ws.Path, "skills", "echoer", ".venv"))
	require.NoError(t, err)
	require.True(t, fi.IsDir())

	// Staging the same skill again should be a no-op when links already exist.
	err = st.StageSkill(ctx, eng, ws, skillRoot, "echoer")
	require.NoError(t, err)
}

func TestStager_StageSkillConcurrentMetadataSafe(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		ctx,
		"stage-skill-concurrent",
		codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Cleanup(ctx, ws)
	})

	const (
		skillCount    = 12
		skillFileName = "SKILL.md"
	)
	root := t.TempDir()
	names := make([]string, 0, skillCount)
	for i := 0; i < skillCount; i++ {
		name := fmt.Sprintf("skill_%02d", i)
		names = append(names, name)
		skillRoot := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(skillRoot, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(skillRoot, skillFileName),
			[]byte(name),
			0o644,
		))
	}

	st := New()
	start := make(chan struct{})
	errs := make(chan error, skillCount)
	for _, name := range names {
		name := name
		go func() {
			<-start
			errs <- st.StageSkill(
				ctx, eng, ws, filepath.Join(root, name), name,
			)
		}()
	}
	close(start)
	for range names {
		require.NoError(t, <-errs)
	}

	raw, err := os.ReadFile(filepath.Join(ws.Path, codeexecutor.MetaFileName))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))

	md, err := st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Len(t, md.Skills, skillCount)
	for _, name := range names {
		meta, ok := md.Skills[name]
		require.True(t, ok)
		require.Equal(t, name, meta.Name)
		require.True(t, meta.Mounted)
		ok, err := st.SkillLinksPresent(ctx, eng, ws, name)
		require.NoError(t, err)
		require.True(t, ok)
	}
}

// TestStager_StageSkillWithOptionsReadOnly exercises the legacy
// ReadOnly staging path used by the phased-out skill_run tool. The
// default path is already covered by TestStager_StageSkillAndLinks;
// this test additionally triggers readOnlyExceptSymlinks so the
// framework does not silently regress the old contract, and gives
// the chmod walk non-trivial coverage.
func TestStager_StageSkillWithOptionsReadOnly(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "stage-skill-ro", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Cleanup(ctx, ws) })

	root := t.TempDir()
	skillRoot := filepath.Join(root, "echoer")
	require.NoError(t, os.MkdirAll(
		filepath.Join(skillRoot, "nested"), 0o755,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillRoot, "SKILL.md"),
		[]byte("body"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillRoot, "nested", "helper.sh"),
		[]byte("echo hi"),
		0o755,
	))

	st := New()
	err = st.StageSkillWithOptions(
		ctx, eng, ws, skillRoot, "echoer",
		StageOptions{ReadOnly: true},
	)
	require.NoError(t, err)

	// Regular files under the staged tree should have the write bit
	// cleared after readOnlyExceptSymlinks runs.
	fi, err := os.Stat(filepath.Join(ws.Path, "skills", "echoer", "SKILL.md"))
	require.NoError(t, err)
	require.Zero(t, fi.Mode()&0o200,
		"read-only staging must clear the owner-write bit on regular files")

	// Symlinks must stay intact.
	ok, err := fsutil.IsLink(filepath.Join(ws.Path, "skills", "echoer", "work"))
	require.NoError(t, err)
	require.True(t, ok)
}

func TestSkillStagingHelpers_EarlyReturns(t *testing.T) {
	st := New()
	ctx := context.Background()

	// Empty skill name returns false, no error.
	ok, err := st.SkillLinksPresent(
		ctx, nil,
		codeexecutor.Workspace{Path: t.TempDir()},
		"",
	)
	require.NoError(t, err)
	require.False(t, ok)

	// Non-existent workspace — links are not present, no error.
	ok, err = st.SkillLinksPresent(
		ctx, nil,
		codeexecutor.Workspace{Path: t.TempDir()},
		"nonexistent",
	)
	require.NoError(t, err)
	require.False(t, ok)

	// Empty target is a no-op.
	require.NoError(
		t,
		st.RemoveWorkspacePath(
			ctx, nil,
			codeexecutor.Workspace{Path: t.TempDir()},
			"",
		),
	)
}

func TestSkillStagingHelpers_DirectFileOps(t *testing.T) {
	st := New()
	ctx := context.Background()

	// linkWorkspaceDirs operates directly on ws.Path.
	dir := t.TempDir()
	ws := codeexecutor.Workspace{ID: "x", Path: dir}

	// Create required workspace dirs.
	require.NoError(t, os.MkdirAll(
		filepath.Join(dir, codeexecutor.DirOut), 0o755,
	))
	require.NoError(t, os.MkdirAll(
		filepath.Join(dir, codeexecutor.DirWork), 0o755,
	))
	require.NoError(t, os.MkdirAll(
		filepath.Join(dir, codeexecutor.DirSkills, "testskill"), 0o755,
	))

	err := st.linkWorkspaceDirs(ctx, nil, ws, "testskill")
	require.NoError(t, err)

	// Verify symlinks are created.
	for _, linkName := range []string{"out", "work", "inputs"} {
		ok, lerr := fsutil.IsLink(filepath.Join(
			dir, codeexecutor.DirSkills, "testskill", linkName,
		))
		require.NoError(t, lerr)
		require.True(t, ok)
	}

	// Verify .venv was created.
	fi, err := os.Stat(filepath.Join(
		dir, codeexecutor.DirSkills, "testskill", ".venv",
	))
	require.NoError(t, err)
	require.True(t, fi.IsDir())

	// RemoveWorkspacePath works on local filesystem.
	require.NoError(t, st.RemoveWorkspacePath(
		ctx, nil, ws, "skills/testskill",
	))
	_, err = os.Stat(filepath.Join(
		dir, codeexecutor.DirSkills, "testskill",
	))
	require.True(t, os.IsNotExist(err))

	// readOnlyExceptSymlinks makes files read-only.
	skillDir := filepath.Join(dir, codeexecutor.DirSkills, "readonly")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillDir, "file.txt"), []byte("x"), 0o644,
	))
	venvDir := filepath.Join(skillDir, skillDirVenv)
	require.NoError(t, os.MkdirAll(venvDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(venvDir, "keep.sh"), []byte("y"), 0o755,
	))

	err = st.readOnlyExceptSymlinks(
		ctx, nil, ws,
		path.Join(codeexecutor.DirSkills, "readonly"),
	)
	require.NoError(t, err)

	// Regular file should be read-only.
	fi, err = os.Stat(filepath.Join(skillDir, "file.txt"))
	require.NoError(t, err)
	require.Zero(t, fi.Mode()&0o200)

	// .venv file should keep write bit.
	fi, err = os.Stat(filepath.Join(venvDir, "keep.sh"))
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&0o200)
}

// pathPrefixRunner prepends a directory to PATH inside bash -lc scripts
// so the login shell's profile cannot clobber the test shim.
type pathPrefixRunner struct {
	inner  codeexecutor.ProgramRunner
	prefix string
}

func (r *pathPrefixRunner) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	if spec.Cmd == "bash" && len(spec.Args) >= 2 && spec.Args[0] == "-lc" {
		args := append([]string(nil), spec.Args...)
		args[1] = "export PATH=" + shellQuote(r.prefix) + ":\"$PATH\"; " + args[1]
		spec.Args = args
	}
	return r.inner.RunProgram(ctx, ws, spec)
}

func chmodDeniedShimDir(t *testing.T) (binDir, marker string) {
	t.Helper()
	binDir = t.TempDir()
	marker = filepath.Join(t.TempDir(), "chmod.called")
	script := "#!/bin/sh\n" +
		"echo called >> " + shellQuote(marker) + "\n" +
		"echo \"chmod: changing permissions of '$2': " +
		"Operation not permitted\" >&2\n" +
		"exit 1\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(binDir, "chmod"),
		[]byte(script),
		0o755,
	))
	return binDir, marker
}

func engineWithChmodDenied(
	t *testing.T,
	rt *localexec.Runtime,
) (codeexecutor.Engine, string) {
	t.Helper()
	binDir, marker := chmodDeniedShimDir(t)
	return codeexecutor.NewEngine(rt, rt, &pathPrefixRunner{
		inner:  rt,
		prefix: binDir,
	}), marker
}

func writeSkillDir(t *testing.T, root, body string) string {
	t.Helper()
	skillRoot := filepath.Join(root, "echoer")
	require.NoError(t, os.MkdirAll(skillRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillRoot, "SKILL.md"),
		[]byte(body),
		0o644,
	))
	return skillRoot
}

func TestRemoveWorkspacePath_ChmodDeniedStillRemoves(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng, marker := engineWithChmodDenied(t, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "remove-chmod-denied", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Cleanup(ctx, ws)
	})

	st := New()
	err = st.StageSkill(
		ctx, eng, ws, writeSkillDir(t, t.TempDir(), "v1"), "echoer",
	)
	require.NoError(t, err)

	dest := filepath.Join(ws.Path, "skills", "echoer")
	require.DirExists(t, dest)

	err = st.RemoveWorkspacePath(ctx, eng, ws, "skills/echoer")
	require.NoError(t, err)
	require.NoDirExists(t, dest)
	_, statErr := os.Stat(marker)
	require.NoError(t, statErr, "chmod shim must have been invoked")
}

func TestRemoveWorkspacePath_RemoveFailureStillReported(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng, _ := engineWithChmodDenied(t, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "remove-rm-denied", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Cleanup(ctx, ws)
	})

	dest := filepath.Join(ws.Path, "skills", "echoer")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dest, "SKILL.md"),
		[]byte("body"),
		0o644,
	))
	// Directory write is required to unlink children. The chmod shim
	// cannot restore u+w, so rm -rf must fail and be reported.
	require.NoError(t, os.Chmod(dest, 0o555))
	t.Cleanup(func() {
		_ = os.Chmod(dest, 0o755)
	})

	err = New().RemoveWorkspacePath(ctx, eng, ws, "skills/echoer")
	require.Error(t, err)
	require.Contains(t, err.Error(), "remove workspace path")
	require.DirExists(t, dest)
}

func TestStager_StageSkill_RestagesWhenChmodDenied(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng, marker := engineWithChmodDenied(t, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "restage-chmod-denied", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Cleanup(ctx, ws)
	})

	st := New()
	err = st.StageSkill(
		ctx, eng, ws, writeSkillDir(t, t.TempDir(), "v1"), "echoer",
	)
	require.NoError(t, err)

	err = st.StageSkill(
		ctx, eng, ws, writeSkillDir(t, t.TempDir(), "v2"), "echoer",
	)
	require.NoError(t, err)

	files, err := rt.Collect(ctx, ws, []string{"skills/echoer/SKILL.md"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "v2", files[0].Content)
	_, statErr := os.Stat(marker)
	require.NoError(t, statErr, "restage must attempt chmod on the old tree")
}
