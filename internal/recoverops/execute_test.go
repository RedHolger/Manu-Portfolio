package recoverops

import "testing"

func TestExecuteOnceRestoresTemplate(t *testing.T) {
	s, p, pol, id := safetyFixture(t)
	out, err := ExecuteOnce(s, p, pol, id)
	if err != nil || out.To != StVerifying {
		t.Fatalf("%+v %v", out, err)
	}
}
