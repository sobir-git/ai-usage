package main

import "testing"

func TestArgumentValidation(t *testing.T) {
	for _, args := range [][]string{{"--timeout", "NaN"}, {"--timeout", "Inf"}, {"--timeout", "1e30"}, {"--timeout", "0"}, {"--retries", "-1"}, {"unexpected"}} {
		if got := run(args); got != 2 {
			t.Fatalf("%v: got %d", args, got)
		}
	}
	if got := run([]string{"--help"}); got != 0 {
		t.Fatalf("help returned %d", got)
	}
}
