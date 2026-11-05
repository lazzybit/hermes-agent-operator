package sshidentity

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The first write lets a competing reconcile win, then submits the stale
// candidate to the real fake client, which enforces resource-version CAS.
type competingClient struct {
	client.Client
	once             sync.Once
	win              func()
	creates, updates int
}

func (c *competingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.creates++
	c.once.Do(c.win)
	return c.Client.Create(ctx, obj, opts...)
}
func (c *competingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.updates++
	c.once.Do(c.win)
	return c.Client.Update(ctx, obj, opts...)
}
func TestEnsureRereadsWinningStateOnConflict(t *testing.T) {
	for _, renew := range []bool{false, true} {
		t.Run(fmt.Sprint("renew=", renew), func(t *testing.T) {
			c, id, now := fixture(t)
			winner := adapter(t, c)
			if renew {
				if _, err := winner.Ensure(context.Background(), id, now); err != nil {
					t.Fatal(err)
				}
				now = now.Add(18 * time.Hour)
			}
			var winning Result
			var winningSecret *corev1.Secret
			race := &competingClient{Client: c}
			race.win = func() {
				var err error
				winning, err = winner.Ensure(context.Background(), id, now)
				if err != nil {
					t.Fatal(err)
				}
				winningSecret = stored(t, c, winning.Secret)
			}
			result, err := adapter(t, race).Ensure(context.Background(), id, now)
			if err != nil {
				t.Fatal(err)
			}
			final := stored(t, c, result.Secret)
			if result.Changed || result.Secret != winning.Secret || final.ResourceVersion != winningSecret.ResourceVersion {
				t.Fatal("loser must reread and reuse winning state")
			}
			for key, value := range winningSecret.Data {
				if !bytes.Equal(value, final.Data[key]) {
					t.Fatal("losing candidate replaced winner")
				}
			}
			if (!renew && race.creates != 1) || (renew && race.updates != 1) {
				t.Fatal("unexpected extra write")
			}
		})
	}
}

type rejectingClient struct {
	client.Client
	writes int
}

func (c *rejectingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.writes++
	return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, obj.GetName())
}
func TestEnsureBoundsConflictsWithoutPublishingCandidate(t *testing.T) {
	c, id, now := fixture(t)
	conflict := &rejectingClient{Client: c}
	result, err := adapter(t, conflict).Ensure(context.Background(), id, now)
	if err == nil || result.Secret.Name != "" || conflict.writes != 5 {
		t.Fatalf("expected five bounded retries and empty result, writes=%d err=%v", conflict.writes, err)
	}
}
func TestEnsureFailsClosedWithoutPrivateErrors(t *testing.T) {
	c, id, now := fixture(t)
	s := adapter(t, c)
	initial, err := s.Ensure(context.Background(), id, now)
	if err != nil {
		t.Fatal(err)
	}
	original := stored(t, c, initial.Secret)
	ca := stored(t, c, client.ObjectKey{Namespace: "test", Name: "ca"})
	for _, scenario := range []string{"wrong-instance", "wrong-namespace", "partial", "corrupt", "foreign-ca"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := id
			secret := original.DeepCopy()
			secret.ResourceVersion = stored(t, c, initial.Secret).ResourceVersion
			secret.Data["unrelated"] = []byte("keep")
			if scenario == "partial" {
				delete(secret.Data, HostCertificate)
			}
			if scenario == "corrupt" {
				secret.Data[UserPrivateKey] = []byte("SENSITIVE-INVALID-KEY")
			}
			if scenario == "foreign-ca" {
				other, _, _ := fixture(t)
				otherCA := stored(t, other, client.ObjectKey{Namespace: "test", Name: "ca"})
				current := ca.DeepCopy()
				current.ResourceVersion = stored(t, c, client.ObjectKeyFromObject(ca)).ResourceVersion
				current.Data = otherCA.Data
				if err := c.Update(context.Background(), current); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Update(context.Background(), secret); err != nil {
				t.Fatal(err)
			}
			if scenario == "wrong-instance" {
				candidate.Instance = "other"
			}
			if scenario == "wrong-namespace" {
				candidate.Namespace = "other"
			}
			before := stored(t, c, initial.Secret)
			result, err := s.Ensure(context.Background(), candidate, now)
			if err == nil || result.Secret.Name != "" {
				t.Fatal("invalid state accepted")
			}
			if strings.Contains(err.Error(), "SENSITIVE") || strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Fatal("private material in error")
			}
			if stored(t, c, initial.Secret).ResourceVersion != before.ResourceVersion {
				t.Fatal("invalid state overwritten")
			}
		})
	}
}
