package isureal

import "testing"

func TestMaxDBConnsFitsPrimaryBudget(t *testing.T) {
	const maxConnections = 300
	if 3*maxDBConns > maxConnections {
		t.Fatalf("3*%d=%d exceeds max_connections=%d", maxDBConns, 3*maxDBConns, maxConnections)
	}
}
