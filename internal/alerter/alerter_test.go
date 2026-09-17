package alerter

import (
	"testing"

	"webchecker/internal/models"
)

func TestEvaluateSlowAlertAfterThreshold(t *testing.T) {
	mon := models.Monitor{FailThreshold: 2, SlowThresholdMS: 100}
	check := models.Check{OK: true, Slow: true, ResponseMS: 250}
	state := models.AlertState{}

	next, down, slow, recovery := evaluate(state, mon, check, true)
	if down || slow || recovery || next.ConsecutiveProblems != 1 {
		t.Fatalf("first slow should wait, got down=%v slow=%v recovery=%v consecutive=%d", down, slow, recovery, next.ConsecutiveProblems)
	}

	next, down, slow, recovery = evaluate(next, mon, check, true)
	if down || recovery || !slow {
		t.Fatalf("second slow should alert, got down=%v slow=%v recovery=%v", down, slow, recovery)
	}
}

func TestEvaluateSlowDisabled(t *testing.T) {
	mon := models.Monitor{FailThreshold: 1, SlowThresholdMS: 100}
	check := models.Check{OK: true, Slow: true, ResponseMS: 250}
	_, down, slow, recovery := evaluate(models.AlertState{}, mon, check, false)
	if down || slow || recovery {
		t.Fatalf("slow alerts disabled, got down=%v slow=%v recovery=%v", down, slow, recovery)
	}
}

func TestEvaluateSlowEqualsThreshold(t *testing.T) {
	mon := models.Monitor{FailThreshold: 1, SlowThresholdMS: 200}
	check := models.Check{OK: true, Slow: true, ResponseMS: 200}
	_, _, slow, _ := evaluate(models.AlertState{}, mon, check, true)
	if !slow {
		t.Fatal("expected slow alert when response equals threshold")
	}
}
