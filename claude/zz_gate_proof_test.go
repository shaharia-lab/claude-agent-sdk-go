package claude

import "testing"

func   TestGateProof_DeliberatelyFailing( t *testing.T ) {
	t.Fatal( "deliberate failure: proving CI gate is real (issue #39)" )
}
