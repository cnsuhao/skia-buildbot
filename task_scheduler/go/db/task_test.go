package db

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"sort"
	"testing"
	"time"

	swarming_api "github.com/luci/luci-go/common/api/swarming/swarming/v1"
	assert "github.com/stretchr/testify/require"

	"go.skia.org/infra/go/swarming"
	"go.skia.org/infra/go/testutils"
)

func TestCopyTaskKey(t *testing.T) {
	testutils.SmallTest(t)
	v := TaskKey{
		RepoState: RepoState{
			Repo:     "nou.git",
			Revision: "1",
		},
		Name:        "Build",
		ForcedJobId: "123",
	}
	testutils.AssertCopy(t, v, v.Copy())
}

// Test that Task.UpdateFromSwarming returns an error when the input data is
// invalid.
func TestUpdateFromSwarmingInvalid(t *testing.T) {
	testutils.SmallTest(t)
	now := time.Now().UTC().Round(time.Microsecond)
	task := &Task{
		Id: "A",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "A",
				Revision: "A",
			},
			Name:        "A",
			ForcedJobId: "A",
		},
		Created: now,
		Commits: []string{"A", "B"},
	}
	copy := task.Copy()

	testError := func(s *swarming_api.SwarmingRpcsTaskResult, msg string) {
		changed, err := task.UpdateFromSwarming(s)
		assert.False(t, changed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), msg)
	}

	testError(nil, "Missing TaskResult")

	testError(&swarming_api.SwarmingRpcsTaskResult{
		CreatedTs: now.Format(swarming.TIMESTAMP_FORMAT),
		State:     SWARMING_STATE_COMPLETED,
		Tags:      []string{"invalid"},
	}, "Invalid Swarming task tag")

	testError(&swarming_api.SwarmingRpcsTaskResult{
		CreatedTs: "20160817T142302.543490",
		State:     SWARMING_STATE_COMPLETED,
	}, "Unable to parse task creation time")

	testError(&swarming_api.SwarmingRpcsTaskResult{
		CreatedTs: now.Format(swarming.TIMESTAMP_FORMAT),
		State:     SWARMING_STATE_COMPLETED,
		StartedTs: "20160817T142302.543490",
	}, "Unable to parse StartedTs")

	testError(&swarming_api.SwarmingRpcsTaskResult{
		CreatedTs:   now.Format(swarming.TIMESTAMP_FORMAT),
		State:       SWARMING_STATE_COMPLETED,
		CompletedTs: "20160817T142302.543490",
	}, "Unable to parse CompletedTs")

	testError(&swarming_api.SwarmingRpcsTaskResult{
		CreatedTs:   now.Format(swarming.TIMESTAMP_FORMAT),
		State:       SWARMING_STATE_EXPIRED,
		AbandonedTs: "20160817T142302.543490",
	}, "Unable to parse AbandonedTs")

	// Unchanged.
	testutils.AssertDeepEqual(t, task, copy)
}

// Test that Task.UpdateFromSwarming returns an error when the task "identity"
// fields do not match.
func TestUpdateFromSwarmingMismatched(t *testing.T) {
	testutils.SmallTest(t)
	now := time.Now().UTC().Round(time.Microsecond)
	task := &Task{
		Id: "A",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "A",
				Revision: "A",
			},
			Name:        "A",
			ForcedJobId: "A",
		},
		Created:        now,
		Commits:        []string{"A", "B"},
		SwarmingTaskId: "A",
	}
	copy := task.Copy()

	testError := func(s *swarming_api.SwarmingRpcsTaskResult, msg string) {
		changed, err := task.UpdateFromSwarming(s)
		assert.False(t, changed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), msg)
	}

	s := &swarming_api.SwarmingRpcsTaskResult{
		TaskId:    "A",
		CreatedTs: now.Format(swarming.TIMESTAMP_FORMAT),
		Failure:   false,
		State:     SWARMING_STATE_COMPLETED,
		Tags: []string{
			fmt.Sprintf("%s:B", SWARMING_TAG_ID),
			fmt.Sprintf("%s:A", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:A", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:A", SWARMING_TAG_REVISION),
		},
	}
	testError(s, "Id does not match")

	s.Tags[0] = fmt.Sprintf("%s:A", SWARMING_TAG_ID)
	s.Tags[1] = fmt.Sprintf("%s:B", SWARMING_TAG_NAME)
	testError(s, "Name does not match")

	s.Tags[1] = fmt.Sprintf("%s:A", SWARMING_TAG_NAME)
	s.Tags[2] = fmt.Sprintf("%s:B", SWARMING_TAG_REPO)
	testError(s, "Repo does not match")

	s.Tags[2] = fmt.Sprintf("%s:A", SWARMING_TAG_REPO)
	s.Tags[3] = fmt.Sprintf("%s:B", SWARMING_TAG_REVISION)
	testError(s, "Revision does not match")

	s.Tags[3] = fmt.Sprintf("%s:A", SWARMING_TAG_REVISION)
	s.CreatedTs = now.Add(time.Hour).Format(swarming.TIMESTAMP_FORMAT)
	testError(s, "Creation time has changed")

	s.CreatedTs = now.Format(swarming.TIMESTAMP_FORMAT)
	s.TaskId = "D"
	testError(s, "Swarming task ID does not match")

	// Unchanged.
	testutils.AssertDeepEqual(t, task, copy)
}

// Test that Task.UpdateFromSwarming sets the expected fields in an empty Task.
func TestUpdateFromSwarmingInit(t *testing.T) {
	testutils.SmallTest(t)
	now := time.Now().UTC().Round(time.Microsecond)
	task1 := &Task{}
	s := &swarming_api.SwarmingRpcsTaskResult{
		TaskId: "E",
		// Include both AbandonedTs and CompletedTs to test that CompletedTs takes
		// precedence.
		AbandonedTs: now.Add(-1 * time.Minute).Format(swarming.TIMESTAMP_FORMAT),
		CreatedTs:   now.Add(-3 * time.Hour).Format(swarming.TIMESTAMP_FORMAT),
		CompletedTs: now.Add(-2 * time.Minute).Format(swarming.TIMESTAMP_FORMAT),
		Failure:     false,
		StartedTs:   now.Add(-time.Hour).Format(swarming.TIMESTAMP_FORMAT),
		State:       SWARMING_STATE_COMPLETED,
		Tags: []string{
			fmt.Sprintf("%s:A", SWARMING_TAG_ID),
			fmt.Sprintf("%s:B", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:C", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:D", SWARMING_TAG_REVISION),
			fmt.Sprintf("%s:E", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:F", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:G", SWARMING_TAG_FORCED_JOB_ID),
		},
		OutputsRef: &swarming_api.SwarmingRpcsFilesRef{
			Isolated: "F",
		},
		BotId: "G",
	}
	changed1, err1 := task1.UpdateFromSwarming(s)
	assert.NoError(t, err1)
	assert.True(t, changed1)
	testutils.AssertDeepEqual(t, task1, &Task{
		Id: "A",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Created:        now.Add(-3 * time.Hour),
		Commits:        nil,
		Started:        now.Add(-time.Hour),
		Finished:       now.Add(-2 * time.Minute),
		Status:         TASK_STATUS_SUCCESS,
		SwarmingTaskId: "E",
		IsolatedOutput: "F",
		SwarmingBotId:  "G",
		ParentTaskIds:  []string{"E", "F"},
	})

	// Repeat to get Finished from AbandonedTs.
	task2 := &Task{}
	s.CompletedTs = ""
	s.State = SWARMING_STATE_EXPIRED
	changed2, err2 := task2.UpdateFromSwarming(s)
	assert.NoError(t, err2)
	assert.True(t, changed2)
	testutils.AssertDeepEqual(t, task2, &Task{
		Id: "A",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Created:        now.Add(-3 * time.Hour),
		Commits:        nil,
		Started:        now.Add(-time.Hour),
		Finished:       now.Add(-time.Minute),
		Status:         TASK_STATUS_MISHAP,
		SwarmingTaskId: "E",
		IsolatedOutput: "F",
		SwarmingBotId:  "G",
		ParentTaskIds:  []string{"E", "F"},
	})
}

// Test that Task.UpdateFromSwarming updates the expected fields in an existing
// Task.
func TestUpdateFromSwarmingUpdate(t *testing.T) {
	testutils.SmallTest(t)
	now := time.Now().UTC().Round(time.Microsecond)
	task := &Task{
		Id: "A",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Created:        now.Add(-3 * time.Hour),
		Commits:        []string{"D", "Z"},
		Started:        now.Add(-2 * time.Hour),
		Finished:       now.Add(-1 * time.Hour),
		Status:         TASK_STATUS_SUCCESS,
		SwarmingTaskId: "E",
		IsolatedOutput: "F",
		SwarmingBotId:  "H",
		ParentTaskIds:  []string{"E", "F"},
	}
	s := &swarming_api.SwarmingRpcsTaskResult{
		TaskId: "E",
		// Include both AbandonedTs and CompletedTs to test that CompletedTs takes
		// precedence.
		AbandonedTs: now.Add(-90 * time.Second).Format(swarming.TIMESTAMP_FORMAT),
		CreatedTs:   now.Add(-3 * time.Hour).Format(swarming.TIMESTAMP_FORMAT),
		CompletedTs: now.Add(-1 * time.Minute).Format(swarming.TIMESTAMP_FORMAT),
		Failure:     true,
		StartedTs:   now.Add(-2 * time.Minute).Format(swarming.TIMESTAMP_FORMAT),
		State:       SWARMING_STATE_COMPLETED,
		Tags: []string{
			fmt.Sprintf("%s:A", SWARMING_TAG_ID),
			fmt.Sprintf("%s:B", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:C", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:D", SWARMING_TAG_REVISION),
			fmt.Sprintf("%s:E", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:F", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:G", SWARMING_TAG_FORCED_JOB_ID),
		},
		OutputsRef: &swarming_api.SwarmingRpcsFilesRef{
			Isolated: "G",
		},
		BotId: "I",
	}
	changed, err := task.UpdateFromSwarming(s)
	assert.NoError(t, err)
	assert.True(t, changed)
	testutils.AssertDeepEqual(t, task, &Task{
		Id: "A",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Created:        now.Add(-3 * time.Hour),
		Commits:        []string{"D", "Z"},
		Started:        now.Add(-2 * time.Minute),
		Finished:       now.Add(-1 * time.Minute),
		Status:         TASK_STATUS_FAILURE,
		SwarmingTaskId: "E",
		IsolatedOutput: "G",
		SwarmingBotId:  "I",
		ParentTaskIds:  []string{"E", "F"},
	})

	// Make an unrelated change, no change to Task.
	s.ModifiedTs = now.Format(swarming.TIMESTAMP_FORMAT)
	changed, err = task.UpdateFromSwarming(s)
	assert.NoError(t, err)
	assert.False(t, changed)
	testutils.AssertDeepEqual(t, task, &Task{
		Id: "A",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Created:        now.Add(-3 * time.Hour),
		Commits:        []string{"D", "Z"},
		Started:        now.Add(-2 * time.Minute),
		Finished:       now.Add(-1 * time.Minute),
		Status:         TASK_STATUS_FAILURE,
		SwarmingTaskId: "E",
		IsolatedOutput: "G",
		SwarmingBotId:  "I",
		ParentTaskIds:  []string{"E", "F"},
	})

	// Modify so that we get Finished from AbandonedTs.
	s.CompletedTs = ""
	s.State = SWARMING_STATE_EXPIRED
	changed, err = task.UpdateFromSwarming(s)
	assert.NoError(t, err)
	assert.True(t, changed)
	testutils.AssertDeepEqual(t, task, &Task{
		Id: "A",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Created:        now.Add(-3 * time.Hour),
		Commits:        []string{"D", "Z"},
		Started:        now.Add(-2 * time.Minute),
		Finished:       now.Add(-90 * time.Second),
		Status:         TASK_STATUS_MISHAP,
		SwarmingTaskId: "E",
		IsolatedOutput: "G",
		SwarmingBotId:  "I",
		ParentTaskIds:  []string{"E", "F"},
	})
}

// Test that Task.UpdateFromSwarming updates the Status field correctly.
func TestUpdateFromSwarmingUpdateStatus(t *testing.T) {
	testutils.SmallTest(t)
	now := time.Now().UTC().Round(time.Microsecond)

	testUpdateStatus := func(s *swarming_api.SwarmingRpcsTaskResult, newStatus TaskStatus) {
		task := &Task{
			Id: "A",
			TaskKey: TaskKey{
				RepoState: RepoState{
					Repo:     "C",
					Revision: "D",
				},
				Name:        "B",
				ForcedJobId: "G",
			},
			Created:        now.Add(-3 * time.Hour),
			Commits:        []string{"D", "Z"},
			Status:         TASK_STATUS_SUCCESS,
			SwarmingTaskId: "E",
			ParentTaskIds:  []string{"E", "F"},
		}
		changed, err := task.UpdateFromSwarming(s)
		assert.NoError(t, err)
		assert.True(t, changed)
		testutils.AssertDeepEqual(t, task, &Task{
			Id: "A",
			TaskKey: TaskKey{
				RepoState: RepoState{
					Repo:     "C",
					Revision: "D",
				},
				Name:        "B",
				ForcedJobId: "G",
			},
			Created:        now.Add(-3 * time.Hour),
			Commits:        []string{"D", "Z"},
			Status:         newStatus,
			SwarmingTaskId: "E",
			ParentTaskIds:  []string{"E", "F"},
		})
	}

	s := &swarming_api.SwarmingRpcsTaskResult{
		TaskId:    "E",
		CreatedTs: now.Add(-3 * time.Hour).Format(swarming.TIMESTAMP_FORMAT),
		Failure:   false,
		State:     SWARMING_STATE_PENDING,
		Tags: []string{
			fmt.Sprintf("%s:A", SWARMING_TAG_ID),
			fmt.Sprintf("%s:B", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:C", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:D", SWARMING_TAG_REVISION),
			fmt.Sprintf("%s:E", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:F", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:G", SWARMING_TAG_FORCED_JOB_ID),
		},
		OutputsRef: nil,
	}

	testUpdateStatus(s, TASK_STATUS_PENDING)

	s.State = SWARMING_STATE_RUNNING
	testUpdateStatus(s, TASK_STATUS_RUNNING)

	for _, state := range []string{SWARMING_STATE_BOT_DIED, SWARMING_STATE_CANCELED, SWARMING_STATE_EXPIRED, SWARMING_STATE_TIMED_OUT} {
		s.State = state
		testUpdateStatus(s, TASK_STATUS_MISHAP)
	}

	s.State = SWARMING_STATE_COMPLETED
	s.Failure = true
	testUpdateStatus(s, TASK_STATUS_FAILURE)
}

func TestUpdateDBFromSwarmingTask(t *testing.T) {
	testutils.SmallTest(t)
	db := NewInMemoryTaskDB()

	// Create task, initialize from swarming, and save.
	now := time.Now().UTC().Round(time.Microsecond)
	task := &Task{
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Commits:       []string{"D", "Z"},
		Status:        TASK_STATUS_PENDING,
		ParentTaskIds: []string{"E", "F"},
	}
	assert.NoError(t, db.AssignId(task))

	s := &swarming_api.SwarmingRpcsTaskResult{
		TaskId:    "E", // This is the Swarming TaskId.
		CreatedTs: now.Add(time.Second).Format(swarming.TIMESTAMP_FORMAT),
		State:     SWARMING_STATE_PENDING,
		Tags: []string{
			fmt.Sprintf("%s:%s", SWARMING_TAG_ID, task.Id),
			fmt.Sprintf("%s:B", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:C", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:D", SWARMING_TAG_REVISION),
			fmt.Sprintf("%s:E", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:F", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:G", SWARMING_TAG_FORCED_JOB_ID),
		},
	}
	modified, err := task.UpdateFromSwarming(s)
	assert.NoError(t, err)
	assert.True(t, modified)
	assert.NoError(t, db.PutTask(task))

	// Get update from Swarming.
	s.StartedTs = now.Add(time.Minute).Format(swarming.TIMESTAMP_FORMAT)
	s.CompletedTs = now.Add(2 * time.Minute).Format(swarming.TIMESTAMP_FORMAT)
	s.State = SWARMING_STATE_COMPLETED
	s.Failure = true
	s.OutputsRef = &swarming_api.SwarmingRpcsFilesRef{
		Isolated: "G",
	}
	s.BotId = "H"

	assert.NoError(t, UpdateDBFromSwarmingTask(db, s))

	updatedTask, err := db.GetTaskById(task.Id)
	assert.NoError(t, err)
	testutils.AssertDeepEqual(t, updatedTask, &Task{
		Id: task.Id,
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Created:        now.Add(time.Second),
		Commits:        []string{"D", "Z"},
		Started:        now.Add(time.Minute),
		Finished:       now.Add(2 * time.Minute),
		Status:         TASK_STATUS_FAILURE,
		SwarmingTaskId: "E",
		IsolatedOutput: "G",
		SwarmingBotId:  "H",
		ParentTaskIds:  []string{"E", "F"},
		// Use value from updatedTask so they are deep-equal.
		DbModified: updatedTask.DbModified,
	})

	lastDbModified := updatedTask.DbModified

	// Make an unrelated change; assert no change to Task.
	s.ModifiedTs = now.Format(swarming.TIMESTAMP_FORMAT)

	assert.NoError(t, UpdateDBFromSwarmingTask(db, s))
	updatedTask, err = db.GetTaskById(task.Id)
	assert.NoError(t, err)
	assert.True(t, lastDbModified.Equal(updatedTask.DbModified))
}

func TestUpdateDBFromSwarmingTaskTryJob(t *testing.T) {
	testutils.SmallTest(t)
	db := NewInMemoryTaskDB()

	// Create task, initialize from swarming, and save.
	now := time.Now().UTC().Round(time.Microsecond)
	task := &Task{
		TaskKey: TaskKey{
			RepoState: RepoState{
				Patch: Patch{
					Server:   "A",
					Issue:    "B",
					Patchset: "P",
				},
				Repo:     "C",
				Revision: "D",
			},
			Name:        "B",
			ForcedJobId: "G",
		},
		Commits:       []string{"D", "Z"},
		Status:        TASK_STATUS_PENDING,
		ParentTaskIds: []string{"E", "F"},
	}
	assert.NoError(t, db.AssignId(task))

	s := &swarming_api.SwarmingRpcsTaskResult{
		TaskId:    "E", // This is the Swarming TaskId.
		CreatedTs: now.Add(time.Second).Format(swarming.TIMESTAMP_FORMAT),
		State:     SWARMING_STATE_PENDING,
		Tags: []string{
			fmt.Sprintf("%s:%s", SWARMING_TAG_ID, task.Id),
			fmt.Sprintf("%s:B", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:C", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:D", SWARMING_TAG_REVISION),
			fmt.Sprintf("%s:E", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:F", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:G", SWARMING_TAG_FORCED_JOB_ID),
			fmt.Sprintf("%s:A", SWARMING_TAG_SERVER),
			fmt.Sprintf("%s:B", SWARMING_TAG_ISSUE),
			fmt.Sprintf("%s:P", SWARMING_TAG_PATCHSET),
		},
	}
	modified, err := task.UpdateFromSwarming(s)
	assert.NoError(t, err)
	assert.True(t, modified)

	// Make sure we can't change the server, issue, or patchset.
	s = &swarming_api.SwarmingRpcsTaskResult{
		TaskId:    "E", // This is the Swarming TaskId.
		CreatedTs: now.Add(time.Second).Format(swarming.TIMESTAMP_FORMAT),
		State:     SWARMING_STATE_PENDING,
		Tags: []string{
			fmt.Sprintf("%s:%s", SWARMING_TAG_ID, task.Id),
			fmt.Sprintf("%s:B", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:C", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:D", SWARMING_TAG_REVISION),
			fmt.Sprintf("%s:E", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:F", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:G", SWARMING_TAG_FORCED_JOB_ID),
			fmt.Sprintf("%s:BAD", SWARMING_TAG_SERVER),
			fmt.Sprintf("%s:B", SWARMING_TAG_ISSUE),
			fmt.Sprintf("%s:P", SWARMING_TAG_PATCHSET),
		},
	}
	modified, err = task.UpdateFromSwarming(s)
	assert.NotNil(t, err)
	assert.False(t, modified)

	// Make sure we can't change the server, issue, or patchset.
	s = &swarming_api.SwarmingRpcsTaskResult{
		TaskId:    "E", // This is the Swarming TaskId.
		CreatedTs: now.Add(time.Second).Format(swarming.TIMESTAMP_FORMAT),
		State:     SWARMING_STATE_PENDING,
		Tags: []string{
			fmt.Sprintf("%s:%s", SWARMING_TAG_ID, task.Id),
			fmt.Sprintf("%s:B", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:C", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:D", SWARMING_TAG_REVISION),
			fmt.Sprintf("%s:E", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:F", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:G", SWARMING_TAG_FORCED_JOB_ID),
			fmt.Sprintf("%s:A", SWARMING_TAG_SERVER),
			fmt.Sprintf("%s:BAD", SWARMING_TAG_ISSUE),
			fmt.Sprintf("%s:P", SWARMING_TAG_PATCHSET),
		},
	}
	modified, err = task.UpdateFromSwarming(s)
	assert.NotNil(t, err)
	assert.False(t, modified)

	// Make sure we can't change the server, issue, or patchset.
	s = &swarming_api.SwarmingRpcsTaskResult{
		TaskId:    "E", // This is the Swarming TaskId.
		CreatedTs: now.Add(time.Second).Format(swarming.TIMESTAMP_FORMAT),
		State:     SWARMING_STATE_PENDING,
		Tags: []string{
			fmt.Sprintf("%s:%s", SWARMING_TAG_ID, task.Id),
			fmt.Sprintf("%s:B", SWARMING_TAG_NAME),
			fmt.Sprintf("%s:C", SWARMING_TAG_REPO),
			fmt.Sprintf("%s:D", SWARMING_TAG_REVISION),
			fmt.Sprintf("%s:E", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:F", SWARMING_TAG_PARENT_TASK_ID),
			fmt.Sprintf("%s:G", SWARMING_TAG_FORCED_JOB_ID),
			fmt.Sprintf("%s:A", SWARMING_TAG_SERVER),
			fmt.Sprintf("%s:B", SWARMING_TAG_ISSUE),
			fmt.Sprintf("%s:BAD", SWARMING_TAG_PATCHSET),
		},
	}
	modified, err = task.UpdateFromSwarming(s)
	assert.NotNil(t, err)
	assert.False(t, modified)
}

func TestCopyTask(t *testing.T) {
	testutils.SmallTest(t)
	now := time.Now()
	v := &Task{
		Commits:        []string{"a", "b"},
		Created:        now.Add(time.Nanosecond),
		DbModified:     now.Add(time.Millisecond),
		Finished:       now.Add(time.Second),
		Id:             "42",
		IsolatedOutput: "lonely-result",
		ParentTaskIds:  []string{"38", "39", "40"},
		RetryOf:        "41",
		Started:        now.Add(time.Minute),
		Status:         TASK_STATUS_MISHAP,
		SwarmingBotId:  "ENIAC",
		SwarmingTaskId: "abc123",
		TaskKey: TaskKey{
			RepoState: RepoState{
				Repo:     "nou.git",
				Revision: "1",
			},
			Name: "Build",
		},
	}
	testutils.AssertCopy(t, v, v.Copy())
}

// Test that sort.Sort(TaskSlice(...)) works correctly.
func TestTaskSort(t *testing.T) {
	testutils.SmallTest(t)
	tasks := []*Task{}
	addTask := func(ts time.Time) {
		task := &Task{
			Created: ts,
		}
		tasks = append(tasks, task)
	}

	// Add tasks with various creation timestamps.
	addTask(time.Date(2008, time.August, 8, 8, 8, 8, 8, time.UTC))               // 0
	addTask(time.Date(1776, time.July, 4, 13, 0, 0, 0, time.UTC))                // 1
	addTask(time.Date(2016, time.December, 31, 23, 59, 59, 999999999, time.UTC)) // 2
	addTask(time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC))              // 3

	// Manually sort.
	expected := []*Task{tasks[1], tasks[3], tasks[0], tasks[2]}

	sort.Sort(TaskSlice(tasks))

	testutils.AssertDeepEqual(t, expected, tasks)
}

func TestTaskEncoder(t *testing.T) {
	testutils.SmallTest(t)
	// TODO(benjaminwagner): Is there any way to cause an error?
	e := TaskEncoder{}
	expectedTasks := map[*Task][]byte{}
	for i := 0; i < 25; i++ {
		task := &Task{}
		task.Id = fmt.Sprintf("Id-%d", i)
		task.Name = "Bingo-was-his-name-o"
		task.Commits = []string{fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i+1)}
		var buf bytes.Buffer
		err := gob.NewEncoder(&buf).Encode(task)
		assert.NoError(t, err)
		expectedTasks[task] = buf.Bytes()
		assert.True(t, e.Process(task))
	}

	actualTasks := map[*Task][]byte{}
	for task, serialized, err := e.Next(); task != nil; task, serialized, err = e.Next() {
		assert.NoError(t, err)
		actualTasks[task] = serialized
	}
	testutils.AssertDeepEqual(t, expectedTasks, actualTasks)
}

func TestTaskEncoderNoTasks(t *testing.T) {
	testutils.SmallTest(t)
	e := TaskEncoder{}
	task, serialized, err := e.Next()
	assert.NoError(t, err)
	assert.Nil(t, task)
	assert.Nil(t, serialized)
}

func TestTaskDecoder(t *testing.T) {
	testutils.SmallTest(t)
	d := TaskDecoder{}
	expectedTasks := map[string]*Task{}
	for i := 0; i < 250; i++ {
		task := &Task{}
		task.Id = fmt.Sprintf("Id-%d", i)
		task.Name = "Bingo-was-his-name-o"
		task.Commits = []string{fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i+1)}
		task.ParentTaskIds = []string{fmt.Sprintf("Id-%d", i-1)}
		var buf bytes.Buffer
		err := gob.NewEncoder(&buf).Encode(task)
		assert.NoError(t, err)
		expectedTasks[task.Id] = task
		assert.True(t, d.Process(buf.Bytes()))
	}

	actualTasks := map[string]*Task{}
	result, err := d.Result()
	assert.NoError(t, err)
	assert.Equal(t, len(expectedTasks), len(result))
	for _, task := range result {
		actualTasks[task.Id] = task
	}
	testutils.AssertDeepEqual(t, expectedTasks, actualTasks)
}

func TestTaskDecoderNoTasks(t *testing.T) {
	testutils.SmallTest(t)
	d := TaskDecoder{}
	result, err := d.Result()
	assert.NoError(t, err)
	assert.Equal(t, 0, len(result))
}

func TestTaskDecoderError(t *testing.T) {
	testutils.SmallTest(t)
	task := &Task{}
	task.Id = "Id"
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(task)
	assert.NoError(t, err)
	serialized := buf.Bytes()
	invalid := append([]byte("Hi Mom!"), serialized...)

	d := TaskDecoder{}
	// Process should return true before it encounters an invalid result.
	assert.True(t, d.Process(serialized))
	assert.True(t, d.Process(serialized))
	// Process may return true or false after encountering an invalid value.
	_ = d.Process(invalid)
	for i := 0; i < 250; i++ {
		_ = d.Process(serialized)
	}

	// Result should return error.
	result, err := d.Result()
	assert.Error(t, err)
	assert.Equal(t, 0, len(result))
}

func TestCopyTaskSummary(t *testing.T) {
	testutils.SmallTest(t)
	v := &TaskSummary{
		Id:             "123",
		Status:         TASK_STATUS_FAILURE,
		SwarmingTaskId: "abc123",
	}
	testutils.AssertCopy(t, v, v.Copy())
}
