package Launcher

import (
	"pbftnode/source/Blockchain/Socket"
	"reflect"
	"testing"
)

func Test_createDelay(t *testing.T) {
	type args struct {
		delayType       string
		AvgDelay        int
		StdDelay        int
		MatAdjPath      string
		nodeIdInit      int
		nodeIdNeighbour int
	}
	tests := []struct {
		name string
		args args
		want float64
	}{
		{name: "No Node Delay", args: args{
			delayType:       "NoDelay",
			AvgDelay:        2,
			StdDelay:        2,
			MatAdjPath:      "",
			nodeIdInit:      1,
			nodeIdNeighbour: 3,
		}, want: 0},
		{name: "Fix Delay", args: args{
			delayType:       "Fix",
			AvgDelay:        2,
			StdDelay:        2,
			MatAdjPath:      "",
			nodeIdInit:      1,
			nodeIdNeighbour: 3,
		}, want: 2},
		{name: "Fix Delay", args: args{
			delayType:       "Fix",
			AvgDelay:        5,
			StdDelay:        2,
			MatAdjPath:      "",
			nodeIdInit:      1,
			nodeIdNeighbour: 3,
		}, want: 5},
		{name: "Fix Delay from csv", args: args{
			delayType:       "Fix",
			AvgDelay:        5,
			StdDelay:        2,
			MatAdjPath:      "/home/guilain/GolandProjects/guilain-pbftnode/models/matAdj/matAdj.csv",
			nodeIdInit:      1,
			nodeIdNeighbour: 3,
		}, want: 17},
		{name: "Fix Delay from csv same but inverted", args: args{
			delayType:       "Fix",
			AvgDelay:        5,
			StdDelay:        2,
			MatAdjPath:      "/home/guilain/GolandProjects/guilain-pbftnode/models/matAdj/matAdj.csv",
			nodeIdInit:      3,
			nodeIdNeighbour: 1,
		}, want: 17},
		{name: "Fix Delay from csv", args: args{
			delayType:       "Fix",
			AvgDelay:        5,
			StdDelay:        2,
			MatAdjPath:      "/home/guilain/GolandProjects/guilain-pbftnode/models/matAdj/matAdj.csv",
			nodeIdInit:      5,
			nodeIdNeighbour: 25,
		}, want: 31},
		{name: "Fix Delay from csv same but inverted", args: args{
			delayType:       "Fix",
			AvgDelay:        5,
			StdDelay:        2,
			MatAdjPath:      "/home/guilain/GolandProjects/guilain-pbftnode/models/matAdj/matAdj.csv",
			nodeIdInit:      25,
			nodeIdNeighbour: 5,
		}, want: 31},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delayParam := DelayParam{
				tt.args.delayType,
				tt.args.AvgDelay,
				tt.args.StdDelay,
				nil,
				tt.args.MatAdjPath,
			}
			delayParam.loadMatrix()
			delayConfig := createDelay(delayParam, tt.args.nodeIdInit)
			nodeDelay := Socket.NewNodeDelay(delayConfig, true)
			socketDelay := nodeDelay.NewSocketDelay(tt.args.nodeIdNeighbour)
			if got := socketDelay.NewDelay(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("createDelay() = %v, want %v", got, tt.want)
			}
		})
	}
}
