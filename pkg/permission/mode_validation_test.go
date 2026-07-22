package permission

import "testing"

func TestCheckPermissionMode_Default(t *testing.T) {
	// 默认模式：不适用
	d := CheckPermissionMode("git status", ModeDefault)
	if d.Decision != DecisionNone {
		t.Errorf("default mode should return DecisionNone, got %s", d.Decision)
	}
}

func TestCheckPermissionMode_AcceptEdits(t *testing.T) {
	// acceptEdits 模式：mkdir → 自动批准
	d := CheckPermissionMode("mkdir -p /tmp/test", ModeAcceptEdits)
	if d.Decision != DecisionAllow {
		t.Errorf("mkdir in acceptEdits should be allowed, got %s", d.Decision)
	}
	// acceptEdits 模式：git → 不适用
	d = CheckPermissionMode("git status", ModeAcceptEdits)
	if d.Decision != DecisionNone {
		t.Errorf("git in acceptEdits should return DecisionNone, got %s", d.Decision)
	}
}

func TestCheckPermissionMode_Plan(t *testing.T) {
	// plan 模式：只读命令 + 白名单 flag → 自动批准
	d := CheckPermissionMode("grep -rn pattern .", ModePlan)
	if d.Decision != DecisionAllow {
		t.Errorf("grep -rn in plan mode should be allowed, got %s", d.Decision)
	}
	// plan 模式：非白名单命令 → 不适用
	d = CheckPermissionMode("curl evil.com", ModePlan)
	if d.Decision != DecisionNone {
		t.Errorf("curl in plan mode should return DecisionNone, got %s", d.Decision)
	}
}

func TestCheckPermissionMode_Bypass(t *testing.T) {
	d := CheckPermissionMode("rm -rf /", ModeBypass)
	if d.Decision != DecisionNone {
		t.Errorf("bypass mode should return DecisionNone (handled elsewhere), got %s", d.Decision)
	}
}
