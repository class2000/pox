package Socket

import (
	"github.com/rs/zerolog/log"
	"gonum.org/v1/gonum/stat/distuv"
	"math/rand"
	"time"
)

type DelayType uint8

const (
	NoDelaySt DelayType = iota
	NormalDelaySt
	PoissonDelaySt
	FixeDelaySt
)

var delayStMap = map[string]DelayType{
	"NoDelay": NoDelaySt,
	"Normal":  NormalDelaySt,
	"Poisson": PoissonDelaySt,
	"Fix":     FixeDelaySt,
}

func ParseDelayType(behavior string) DelayType {
	c, ok := delayStMap[behavior]
	if !ok {
		log.Error().Msgf("The string %s is not a delay type", behavior)
		return NoDelaySt
	}
	return c
}

func (dType DelayType) delayTypeToProbaDelay(avgDelay float64, stdDelay float64) ProbaDelay {
	if avgDelay == 0 {
		return NoDelay{}
	}
	switch dType {
	case FixeDelaySt:
		return NewFixeDelay(avgDelay)
	case NoDelaySt:
		return NoDelay{}
	case NormalDelaySt:
		return NewNormalDelay(avgDelay, stdDelay)
	case PoissonDelaySt:
		return NewPoissonDelay(avgDelay)
	default:
		return NoDelay{}
	}
}

type ProbaDelay interface {
	NewDelay() float64
	newProbaDelay() ProbaDelay
	copy() ProbaDelay
}

type DelayConfig struct {
	DelayType DelayType
	AvgDelay  int
	StdDelay  int
	// Contains the associated line of the Matrix adjacency
	Matrix []int
}

// NodeDelay Each node contains a unique NodeDelay used to generate other delay for each socket
type NodeDelay struct {
	DelayConfig

	// Set true to have same parameter on all nodes
	standard bool
}

type SocketDelay struct {
	ProbaDelay
}

func NewNodeDelay(delayConfig DelayConfig, isStandard bool) *NodeDelay {
	return &NodeDelay{delayConfig, isStandard}
}

func NewNodeDelayNoDelay() *NodeDelay {
	return &NodeDelay{
		DelayConfig: DelayConfig{
			DelayType: NoDelaySt,
		},
		standard: true,
	}
}

func (node NodeDelay) NewSocketDelay(idNode int) *SocketDelay {
	// In case the node is the dealer
	if idNode == -1 {
		return &SocketDelay{
			ProbaDelay: NoDelay{},
		}
	}
	var avgDelayNode float64
	if node.Matrix == nil {
		avgDelayNode = float64(node.AvgDelay)
	} else {
		if idNode >= len(node.Matrix) {
			log.Panic().Msgf("Index out of range, get id %d on a limit of %d", idNode, len(node.Matrix))
		}
		avgDelayNode = float64(node.Matrix[idNode])
	}
	if node.standard {
		return &SocketDelay{node.DelayType.delayTypeToProbaDelay(avgDelayNode, float64(node.StdDelay))}
	} else {
		newAvgValue := node.DelayType.delayTypeToProbaDelay(avgDelayNode, float64(node.StdDelay)).NewDelay()
		return &SocketDelay{node.DelayType.delayTypeToProbaDelay(newAvgValue, float64(node.StdDelay))}
	}
}

type NormalDelay struct {
	stdDev float64
	mean   float64
}

func NewNormalDelay(mean float64, stdDev float64) NormalDelay {
	return NormalDelay{stdDev: stdDev, mean: mean}
}

func (normal NormalDelay) NewDelay() float64 {
	var delay float64
	for delay <= 0 {
		delay = rand.NormFloat64()*normal.stdDev + normal.mean
	}
	return delay
}

func (normal NormalDelay) newProbaDelay() ProbaDelay {
	return NormalDelay{
		stdDev: normal.stdDev,
		mean:   normal.NewDelay(),
	}
}

func (normal NormalDelay) copy() ProbaDelay {
	return NormalDelay{
		stdDev: normal.stdDev,
		mean:   normal.mean,
	}
}

func (socket SocketDelay) GetSleepNewDelay() time.Duration {
	return time.Duration(socket.NewDelay()) * time.Millisecond
}

func (socket SocketDelay) SleepNewDelay() {
	delay := socket.NewDelay()
	time.Sleep(time.Duration(delay) * time.Millisecond)
}

type NoDelay struct{}

func (delay NoDelay) NewDelay() float64 {
	return 0
}

func (delay NoDelay) newProbaDelay() ProbaDelay {
	return NoDelay{}
}

func (delay NoDelay) copy() ProbaDelay {
	return NoDelay{}
}

type PoissonDelay struct {
	parameter  float64
	poissonLaw distuv.Poisson
}

func NewPoissonDelay(parameter float64) PoissonDelay {
	return PoissonDelay{parameter, distuv.Poisson{
		Lambda: parameter,
	}}
}

func (poisson PoissonDelay) NewDelay() float64 {
	return poisson.poissonLaw.Rand()
}

func (poisson PoissonDelay) newProbaDelay() ProbaDelay {
	return NewPoissonDelay(poisson.NewDelay())
}

func (poisson PoissonDelay) copy() ProbaDelay {
	return NewPoissonDelay(poisson.parameter)
}

type FixeDelay struct {
	parameter float64
}

func NewFixeDelay(parameter float64) FixeDelay {
	return FixeDelay{parameter: parameter}
}

func (delay FixeDelay) NewDelay() float64 {
	return delay.parameter
}

func (delay FixeDelay) newProbaDelay() ProbaDelay {
	return FixeDelay{parameter: delay.parameter}
}

func (delay FixeDelay) copy() ProbaDelay {
	return FixeDelay{parameter: delay.parameter}
}
