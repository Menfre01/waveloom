package permission

import "testing"

func TestGetDestructiveWarning_GitReset(t *testing.T) {
	w := GetDestructiveWarning("git reset --hard HEAD~1")
	if w == "" {
		t.Error("git reset --hard should trigger warning")
	}
}

func TestGetDestructiveWarning_GitPushForce(t *testing.T) {
	w := GetDestructiveWarning("git push --force origin main")
	if w == "" {
		t.Error("git push --force should trigger warning")
	}
}

func TestGetDestructiveWarning_GitClean(t *testing.T) {
	w := GetDestructiveWarning("git clean -fd")
	if w == "" {
		t.Error("git clean -fd should trigger warning")
	}
}

func TestGetDestructiveWarning_RmRf(t *testing.T) {
	w := GetDestructiveWarning("rm -rf /tmp/test")
	if w == "" {
		t.Error("rm -rf should trigger warning")
	}
}

func TestGetDestructiveWarning_DropTable(t *testing.T) {
	w := GetDestructiveWarning("DROP TABLE users;")
	if w == "" {
		t.Error("DROP TABLE should trigger warning")
	}
	// 大小写不敏感
	w = GetDestructiveWarning("Truncate Table logs;")
	if w == "" {
		t.Error("TRUNCATE TABLE should trigger warning")
	}
}

func TestGetDestructiveWarning_KubectlDelete(t *testing.T) {
	w := GetDestructiveWarning("kubectl delete pod myapp")
	if w == "" {
		t.Error("kubectl delete should trigger warning")
	}
}

func TestGetDestructiveWarning_TerraformDestroy(t *testing.T) {
	w := GetDestructiveWarning("terraform destroy -auto-approve")
	if w == "" {
		t.Error("terraform destroy should trigger warning")
	}
}

func TestGetDestructiveWarning_Shutdown(t *testing.T) {
	w := GetDestructiveWarning("shutdown -h now")
	if w == "" {
		t.Error("shutdown should trigger warning")
	}
}

func TestGetDestructiveWarning_SafeCommand(t *testing.T) {
	w := GetDestructiveWarning("git status")
	if w != "" {
		t.Errorf("git status should NOT trigger warning, got %q", w)
	}
	w = GetDestructiveWarning("ls -la")
	if w != "" {
		t.Errorf("ls should NOT trigger warning, got %q", w)
	}
}
