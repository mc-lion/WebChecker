package alerter

import (
	"testing"

	"webchecker/internal/models"
)

func slowMon(failN, slowN int) models.Monitor {
	return models.Monitor{
		FailThreshold:     failN,
		SlowFailThreshold: slowN,
		SlowThresholdMS:   100,
		SlowAlertsEnabled: true,
	}
}

func TestEvaluateSlowAlertAfterThreshold(t *testing.T) {
	mon := slowMon(2, 2)
	check := models.Check{OK: true, Slow: true, ResponseMS: 250}
	state := models.AlertState{}

	next, down, slow, recovery := evaluate(state, mon, check, true)
	if down || slow || recovery || next.ConsecutiveSlow != 1 {
		t.Fatalf("first slow should wait, got down=%v slow=%v recovery=%v consecutive_slow=%d", down, slow, recovery, next.ConsecutiveSlow)
	}

	next, down, slow, recovery = evaluate(next, mon, check, true)
	if down || recovery || !slow {
		t.Fatalf("second slow should alert, got down=%v slow=%v recovery=%v", down, slow, recovery)
	}
	if next.ConsecutiveSlow != 2 {
		t.Fatalf("consecutive_slow=%d, want 2", next.ConsecutiveSlow)
	}
}

func TestEvaluateSlowDisabledGlobal(t *testing.T) {
	mon := slowMon(1, 1)
	check := models.Check{OK: true, Slow: true, ResponseMS: 250}
	_, down, slow, recovery := evaluate(models.AlertState{}, mon, check, false)
	if down || slow || recovery {
		t.Fatalf("slow alerts disabled globally, got down=%v slow=%v recovery=%v", down, slow, recovery)
	}
}

func TestEvaluateSlowDisabledOnMonitor(t *testing.T) {
	mon := slowMon(1, 1)
	mon.SlowAlertsEnabled = false
	check := models.Check{OK: true, Slow: true, ResponseMS: 250}
	next, down, slow, recovery := evaluate(models.AlertState{}, mon, check, true)
	if down || slow || recovery {
		t.Fatalf("slow alerts disabled on monitor, got down=%v slow=%v recovery=%v", down, slow, recovery)
	}
	if next.ConsecutiveSlow != 0 {
		t.Fatalf("consecutive_slow should stay 0 when disabled, got %d", next.ConsecutiveSlow)
	}
}

func TestEvaluateTimeoutSendsOnlyDown(t *testing.T) {
	mon := slowMon(1, 1)
	check := models.Check{OK: false, Slow: true, ResponseMS: 10000, ErrorText: "context deadline exceeded"}
	_, down, slow, recovery := evaluate(models.AlertState{}, mon, check, true)
	if !down {
		t.Fatal("timeout must send down alert")
	}
	if slow {
		t.Fatal("timeout must not also send slow alert")
	}
	if recovery {
		t.Fatal("unexpected recovery")
	}
}

func TestEvaluateSlowEqualsThreshold(t *testing.T) {
	mon := slowMon(1, 1)
	check := models.Check{OK: true, Slow: true, ResponseMS: 200}
	mon.SlowThresholdMS = 200
	_, _, slow, _ := evaluate(models.AlertState{}, mon, check, true)
	if !slow {
		t.Fatal("expected slow alert when response equals threshold")
	}
}

func TestEvaluateDownAndSlowThresholdsIndependent(t *testing.T) {
	mon := slowMon(3, 2)
	slowCheck := models.Check{OK: true, Slow: true, ResponseMS: 500}
	downCheck := models.Check{OK: false, Slow: false, ResponseMS: 10}

	state := models.AlertState{}
	state, _, slow, _ := evaluate(state, mon, slowCheck, true)
	if slow {
		t.Fatal("first slow should not alert yet")
	}
	state, _, slow, _ = evaluate(state, mon, slowCheck, true)
	if !slow {
		t.Fatal("second slow should alert")
	}
	if state.ConsecutiveProblems != 0 {
		t.Fatalf("slow path must reset consecutive_problems, got %d", state.ConsecutiveProblems)
	}

	// После slow: счётчик down должен начинаться с нуля.
	state.SlowAlerted = true
	state, down, _, _ := evaluate(state, mon, downCheck, true)
	if down {
		t.Fatal("first down should not alert with FailThreshold=3")
	}
	if state.ConsecutiveProblems != 1 || state.ConsecutiveSlow != 0 {
		t.Fatalf("after down: problems=%d slow=%d", state.ConsecutiveProblems, state.ConsecutiveSlow)
	}
}
