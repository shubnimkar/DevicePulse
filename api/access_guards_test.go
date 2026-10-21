package main

import "testing"

func TestAccessGuards(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"edit own role", checkRoleChange("a", "a", RoleAdmin, RoleViewer, true, 2), errSelfChange},
		{"demote last admin", checkRoleChange("a", "b", RoleAdmin, RoleManager, true, 1), errLastAdmin},
		{"demote one of two admins", checkRoleChange("a", "b", RoleAdmin, RoleManager, true, 2), nil},
		{"promote viewer", checkRoleChange("a", "b", RoleViewer, RoleAdmin, true, 1), nil},
		{"demote already-disabled admin", checkRoleChange("a", "b", RoleAdmin, RoleViewer, false, 1), nil},
		{"deactivate self", checkDeactivate("a", "a", RoleManager, true, 3), errSelfChange},
		{"deactivate last admin", checkDeactivate("a", "b", RoleAdmin, true, 1), errLastAdmin},
		{"deactivate manager", checkDeactivate("a", "b", RoleManager, true, 1), nil},
	}
	for _, c := range cases {
		if c.err != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.err, c.want)
		}
	}
}
