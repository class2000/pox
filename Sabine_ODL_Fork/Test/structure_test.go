package Test

import (
	"pbftnode/source/Blockchain"
	"testing"
)

func TestFixQueue(t *testing.T) {
	tests := []struct {
		name          string
		list          Blockchain.Queue
		valueInserted []float64
		expectetion   float64
	}{
		{"More than 2", Blockchain.NewFixSizeQueue(5), []float64{1, 1, 1, 1, 1}, 1},
		{"More data than expected", Blockchain.NewFixSizeQueue(5), []float64{0, 0, 01, 1, 1, 1, 1}, 1},
		{"Equal to 2", Blockchain.NewFixSizeQueue(2), []float64{1, 2, 3, 4}, 3.5},
		{"More than 2", Blockchain.NewFixTor(5), []float64{1, 1, 1, 1, 1}, 1},
		{"More data than expected", Blockchain.NewFixTor(5), []float64{0, 0, 01, 1, 1, 1, 1}, 1},
		{"Equal to 2", Blockchain.NewFixTor(2), []float64{1, 2, 3, 4}, 3.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := tt.list
			for _, i := range tt.valueInserted {
				queue.Append(i)
			}
			if queue.Mean() != tt.expectetion {
				t.Errorf("Not the mean expected, got %f, expected %f", queue.Mean(), tt.expectetion)
			}
		})
	}
}
