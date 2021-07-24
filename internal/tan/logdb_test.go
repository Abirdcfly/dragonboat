// Copyright 2017-2021 Lei Ni (nilei81@gmail.com)
//
// This is a proprietary library. You are not allowed to store, read, use,
// modify or redistribute this library without written consent from its
// copyright owners.

package tan

import (
	"bytes"
	"flag"
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/lni/dragonboat/v3/config"
	pb "github.com/lni/dragonboat/v3/raftpb"
	"github.com/lni/goutils/leaktest"
	"github.com/lni/vfs"
	"github.com/stretchr/testify/require"
)

var spawnChild = flag.Bool("spawn-child", false, "spawned child")

func spawn(execName string) ([]byte, error) {
	return exec.Command(execName, "-spawn-child",
		"-test.v", "-test.run=TestFileLock$").CombinedOutput()
}

func TestFileLock(t *testing.T) {
	dbdir := "db-dir"
	child := *spawnChild
	msg := "failed to lock tan dir"
	cfg := config.NodeHostConfig{
		Expert: config.ExpertConfig{
			FS: vfs.Default,
		},
	}
	if !child {
		ldb, err := CreateTan(cfg, nil, []string{dbdir}, nil)
		require.NoError(t, err)
		defer func() {
			ldb.Close()
			require.NoError(t, ldb.fs.RemoveAll(dbdir))
		}()
		out, err := spawn(os.Args[0])
		if err == nil {
			t.Fatalf("file lock didn't prevent the second tan to start, %s", out)
		}
		require.True(t, bytes.Contains(out, []byte(msg)))
	} else {
		ldb, err := CreateTan(cfg, nil, []string{dbdir}, nil)
		if err == nil {
			ldb.Close()
		} else {
			t.Fatalf(msg)
		}
	}
}

func TestListNodeInfo(t *testing.T) {
	defer leaktest.AfterTest(t)()
	cfg := config.NodeHostConfig{
		Expert: config.ExpertConfig{
			FS: vfs.Default,
		},
	}
	ldb, err := CreateTan(cfg, nil, []string{"db-dir"}, nil)
	require.NoError(t, err)
	defer func() {
		ldb.Close()
		require.NoError(t, ldb.fs.RemoveAll("db-dir"))
	}()
	rec := pb.Bootstrap{}
	require.NoError(t, ldb.SaveBootstrapInfo(1, 1, rec))
	require.NoError(t, ldb.SaveBootstrapInfo(2, 2, rec))
	require.NoError(t, ldb.SaveBootstrapInfo(3, 3, rec))
	nodes, err := ldb.ListNodeInfo()
	require.NoError(t, err)
	require.Equal(t, 3, len(nodes))
	for _, n := range nodes {
		require.True(t, n.ClusterID == 1 && n.NodeID == 1 ||
			n.ClusterID == 2 && n.NodeID == 2 ||
			n.ClusterID == 3 && n.NodeID == 3)
	}
}

func TestLogDBCanBeCreated(t *testing.T) {
	defer leaktest.AfterTest(t)()
	cfg := config.NodeHostConfig{
		Expert: config.ExpertConfig{FS: vfs.NewMem()},
	}
	dirs := []string{"db-dir"}
	ldb, err := CreateTan(cfg, nil, dirs, []string{})
	require.Equal(t, tanLogDBName, ldb.Name())
	require.NoError(t, err)
	require.NoError(t, ldb.Close())
}

func TestSaveSnapshots(t *testing.T) {
	defer leaktest.AfterTest(t)()
	cfg := config.NodeHostConfig{
		Expert: config.ExpertConfig{FS: vfs.NewMem()},
	}
	dirs := []string{"db-dir"}
	ldb, err := CreateTan(cfg, nil, dirs, []string{})
	require.NoError(t, err)
	updates := []pb.Update{
		{
			ClusterID: 1,
			NodeID:    1,
			Snapshot:  pb.Snapshot{Index: 100, Term: 10},
		},
		{
			ClusterID: 2,
			NodeID:    1,
			Snapshot:  pb.Snapshot{Index: 200, Term: 10},
		},
	}
	require.NoError(t, ldb.SaveSnapshots(updates))
	ss1, err := ldb.GetSnapshot(1, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(100), ss1.Index)
	ss2, err := ldb.GetSnapshot(2, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(200), ss2.Index)
	require.NoError(t, ldb.Close())
}

func TestSaveRaftState(t *testing.T) {
	defer leaktest.AfterTest(t)()
	cfg := config.NodeHostConfig{
		Expert: config.ExpertConfig{FS: vfs.NewMem()},
	}
	dirs := []string{"db-dir"}
	ldb, err := CreateTan(cfg, nil, dirs, []string{})
	require.NoError(t, err)
	updates := []pb.Update{
		{
			ClusterID: 1,
			NodeID:    1,
			Snapshot:  pb.Snapshot{Index: 100, Term: 10},
			State:     pb.State{Commit: 100, Term: 10},
			EntriesToSave: []pb.Entry{
				{Index: 99, Term: 10},
				{Index: 100, Term: 10},
			},
		},
		{
			ClusterID: 17,
			NodeID:    1,
			Snapshot:  pb.Snapshot{Index: 200, Term: 10},
			State:     pb.State{Commit: 200, Term: 10},
			EntriesToSave: []pb.Entry{
				{Index: 198, Term: 10},
				{Index: 199, Term: 10},
				{Index: 200, Term: 10},
			},
		},
	}
	require.NoError(t, ldb.SaveRaftState(updates, 1))
	ss1, err := ldb.GetSnapshot(1, 1)
	require.NoError(t, err)
	require.Equal(t, updates[0].Snapshot, ss1)

	ss2, err := ldb.GetSnapshot(17, 1)
	require.NoError(t, err)
	require.Equal(t, updates[1].Snapshot, ss2)

	var entries []pb.Entry
	results, _, err := ldb.IterateEntries(entries, 0, 1, 1, 99, 101, math.MaxUint64)
	require.NoError(t, err)
	require.Equal(t, 2, len(results))

	rs, err := ldb.ReadRaftState(1, 1, 98)
	require.NoError(t, err)
	require.Equal(t, updates[0].State, rs.State)
	require.NoError(t, ldb.Close())
}

func TestConcurrentSaveRaftState(t *testing.T) {
	defer leaktest.AfterTest(t)()
	cfg := config.NodeHostConfig{
		Expert: config.ExpertConfig{FS: vfs.NewMem()},
	}
	dirs := []string{"db-dir"}
	ldb, err := CreateLogMultiplexedTan(cfg, nil, dirs, []string{})
	require.NoError(t, err)
	defer ldb.Close()
	for i := uint64(0); i < 16; i++ {
		updates := []pb.Update{
			{
				ClusterID: 1,
				NodeID:    1,
				Snapshot:  pb.Snapshot{Index: i * uint64(100), Term: 10},
				State:     pb.State{Commit: i * uint64(100), Term: 10},
				EntriesToSave: []pb.Entry{
					{Index: i*2 + 1, Term: 10},
					{Index: i*2 + 2, Term: 10},
				},
			},
			{
				ClusterID: 17,
				NodeID:    1,
				Snapshot:  pb.Snapshot{Index: i * uint64(200), Term: 20},
				State:     pb.State{Commit: i * uint64(200), Term: 20},
				EntriesToSave: []pb.Entry{
					{Index: i*3 + 1, Term: 20},
					{Index: i*3 + 2, Term: 20},
					{Index: i*3 + 3, Term: 20},
				},
			},
		}
		require.NoError(t, ldb.SaveRaftState(updates, 1))
	}
	for i := uint64(0); i < 16; i++ {
		updates := []pb.Update{
			{
				ClusterID: 2,
				NodeID:    1,
				Snapshot:  pb.Snapshot{Index: i * uint64(100), Term: 30},
				State:     pb.State{Commit: i * uint64(100), Term: 30},
				EntriesToSave: []pb.Entry{
					{Index: i*2 + 1, Term: 30},
					{Index: i*2 + 2, Term: 30},
				},
			},
			{
				ClusterID: 18,
				NodeID:    1,
				Snapshot:  pb.Snapshot{Index: i * uint64(100), Term: 40},
				State:     pb.State{Commit: i * uint64(100), Term: 40},
				EntriesToSave: []pb.Entry{
					{Index: i * 3, Term: 40},
					{Index: i*3 + 1, Term: 40},
					{Index: i*3 + 2, Term: 40},
				},
			},
		}
		require.NoError(t, ldb.SaveRaftState(updates, 2))
	}
	// TODO: add checks to see whether there are shard directories named as
	// shard-1 and shard-2
	var entries []pb.Entry
	results, _, err := ldb.IterateEntries(entries, 0, 1, 1, 1, 33, math.MaxUint64)
	require.NoError(t, err)
	require.Equal(t, 32, len(results))
	var entries2 []pb.Entry
	results, _, err = ldb.IterateEntries(entries2, 0, 17, 1, 1, 49, math.MaxUint64)
	require.NoError(t, err)
	require.Equal(t, 48, len(results))
	var entries3 []pb.Entry
	results, _, err = ldb.IterateEntries(entries3, 0, 2, 1, 1, 33, math.MaxUint64)
	require.NoError(t, err)
	require.Equal(t, 32, len(results))

	ss1, err := ldb.GetSnapshot(1, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1500), ss1.Index)
	ss2, err := ldb.GetSnapshot(17, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(3000), ss2.Index)
	ss3, err := ldb.GetSnapshot(2, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(30), ss3.Term)
	require.Equal(t, uint64(1500), ss3.Index)
}
