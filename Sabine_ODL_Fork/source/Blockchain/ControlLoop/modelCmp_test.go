package ControlLoop

import (
	"pbftnode/source/config"
	"testing"
)

func TestModelVal_getIdealNbVal(t *testing.T) {
	testModel := ModelVal2D{model: []sample{
		//{4,29.981793610828124},
		{4, 29.997090337254903},
		{5, 29.997090337254903},
		{6, 29.997090337254903},
		{7, 29.99138888888889},
		{8, 29.96814814814815},
		{9, 29.955462962962958},
		{10, 29.925694444444446},
		{11, 29.701388888888886},
		{12, 29.587962962962962},
		{13, 29.27722222222222},
		{14, 29.10138888888889},
		{15, 28.863055555555558},
		{16, 28.498703703703708},
		{17, 28.156388888888888},
		{18, 27.97435185185185},
		{19, 27.55944444444445},
		{20, 27.14875},
	}}

	testModel.postImport()

	tests := []struct {
		name            string
		fields          ModelVal2D
		inputThroughput float64
		want            int
	}{
		{"middle", testModel, 30, 6},
		{"middle", testModel, 29, 14},
		{"middle", testModel, 28, 17},
		{"middle", testModel, 26, 20},
		{"middle", testModel, 27.4, 19},
		{"middle", testModel, 29.997090337254903, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := ModelVal2D{
				model: tt.fields.model,
			}
			if got := model.GetIdealNbVal(tt.inputThroughput, 0, 0, 0); got != tt.want {
				t.Errorf("GetIdealNbVal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestImportCSV(t *testing.T) {
	test := NewModelVal("/home/leduc/GolandProjects/PBFT_GO_Implem/models/model/modele3DG5G.csv")
	test3D, _ := test.(*ModelVal3D)
	test3D.print()
}

func TestModelVal3D_getLatencyIndexFrom(t *testing.T) {
	modele := NewModelVal("/home/leduc/GolandProjects/PBFT_GO_Implem/refineData/exploreData/3DmapRef.csv")
	modele3 := modele.(*ModelVal3D)
	tests := []struct {
		name                string
		initialNbVal        int
		committedThroughput float64
		expectedLatency     int
	}{
		{"Test1", 21, 47, 0},
		{"Test2", 21, 44, 0},
		{"Test3", 21, 42, 5},
		{"Test4", 16, 40, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nbValIndex := modele3.getNbValIndex(tt.initialNbVal)
			latencyIndex := modele3.getLatencyIndexFrom(nbValIndex, tt.committedThroughput)
			latency := modele3.listLag[latencyIndex]
			if latency != tt.expectedLatency {
				t.Errorf("Expected %d, got %d", tt.expectedLatency, latency)
			}
		})
	}
}

func TestModelVal3D_getNbValIndexFrom(t *testing.T) {
	modele := NewModelVal("/home/leduc/GolandProjects/PBFT_GO_Implem/refineData/exploreData/3DmapRef.csv")
	modele3 := modele.(*ModelVal3D)
	tests := []struct {
		name                string
		initialLatency      int
		requestedThroughput float64
		expectedNbVal       int
	}{
		{"Test1", 45, 32, 5},
		{"Test2", 20, 32, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lagIndex := modele3.getLatencylIndex(tt.initialLatency)
			nbValIndex := modele3.getNbValIndexFrom(lagIndex, tt.requestedThroughput)
			nbVal := modele3.listVal[nbValIndex]
			if nbVal != tt.expectedNbVal {
				t.Errorf("Expected %d, got %d", tt.expectedNbVal, nbVal)
			}
		})
	}
}

func TestModelVal3D_GetIdealNbVal(t *testing.T) {
	modele := NewModelVal("/home/leduc/GolandProjects/PBFT_GO_Implem/refineData/exploreData/3DmapRef.csv")
	tests := []struct {
		name                string
		initialNbVal        int
		requestedThroughput float64
		committedThroughput float64
		expectedNbVal       int
	}{
		{"No change needed, no Latency", 21, 46, 46 / config.BlockSize, 21},
		{"Increase of Tx, no Latency", 21, 47, 46 / config.BlockSize, 20},
		{"Random Point", 27, 30, 34 / config.BlockSize, 35},
		{"Max NbNode", 50, 15, 103 / config.BlockSize, 50},
		{"Max Lat", 4, 15, 11 / config.BlockSize, 4},
		//{"From exp", 50, 47.166667, 22.616667, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idealNbVal := modele.GetIdealNbVal(tt.requestedThroughput, 0, tt.committedThroughput, tt.initialNbVal)
			if idealNbVal != tt.expectedNbVal {
				t.Errorf("Expected %d, got %d", tt.expectedNbVal, idealNbVal)
			}
		})
	}
}
