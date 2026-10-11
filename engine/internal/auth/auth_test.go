// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"path/filepath"
	"testing"
)

func TestRootAdminUserLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, root, err := Open(filepath.Join(dir, "a.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if root == "" || len(root) < 32 {
		t.Fatalf("root token too short")
	}
	role, id, err := store.Authenticate(root)
	if err != nil || role != RoleRoot || id != "root" {
		t.Fatalf("root auth: %v %s %s", err, role, id)
	}
	if !Has(role, CapTokenManage) {
		t.Fatal("root should manage tokens")
	}

	aid, aplain, err := store.CreateToken(RoleAdmin, "ops")
	if err != nil {
		t.Fatal(err)
	}
	role, id, err = store.Authenticate(aplain)
	if err != nil || role != RoleAdmin || id != aid {
		t.Fatalf("admin auth: %v", err)
	}

	uid, uplain, err := store.CreateToken(RoleUser, "desk")
	if err != nil {
		t.Fatal(err)
	}
	role, _, err = store.Authenticate(uplain)
	if err != nil || role != RoleUser || Has(role, CapTokenManage) {
		t.Fatalf("user caps wrong: %v %v", err, role)
	}

	list, err := store.ListTokens()
	if err != nil || len(list) < 3 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	for _, m := range list {
		if m.Role == RoleRoot && (m.Prefix != "" || !m.Masked) {
			t.Fatalf("root must be masked without prefix: %+v", m)
		}
	}
	if err := store.DeleteToken("root"); err != ErrImmutable {
		t.Fatalf("delete root: %v", err)
	}
	if err := store.DeleteToken(uid); err != nil {
		t.Fatal(err)
	}
	store.AddStat("asr_streams", 2)
	st := store.SnapshotStats()
	if st.ASRStreams != 2 {
		t.Fatalf("stats: %+v", st)
	}
}
