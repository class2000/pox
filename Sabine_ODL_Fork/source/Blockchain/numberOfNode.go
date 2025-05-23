package Blockchain

type ValidatorGoroutine struct {
	CloseHandler
	nodeNumber int
	inChange   chan chan int
	inGetInt   chan chan<- int
}

func (node *ValidatorGoroutine) GetNumberOfNode() int {
	canal := make(chan int, 1)
	node.inGetInt <- canal
	nbr := <-canal
	close(canal)
	return nbr
}

func (node *ValidatorGoroutine) SetNumberOfNode(newNbr int) bool {
	canal := make(chan int, 1)
	canal <- newNbr
	node.inChange <- canal
	status := <-canal > 0
	close(canal)
	return status
}

func (node *ValidatorGoroutine) setNumberOfNodeRoutine(canal chan int) {
	newNbr := <-canal
	node.nodeNumber = newNbr
	canal <- 1
}

func (node *ValidatorGoroutine) Close() {
	node.StopLoop()
	close(node.inChange)
	close(node.inGetInt)
	return
}

func newNodeNumberStr(initNumber int) *ValidatorGoroutine {
	nodeNumber := &ValidatorGoroutine{
		CloseHandler: NewCloseHandler(),
		nodeNumber:   initNumber,
		inChange:     make(chan chan int),
		inGetInt:     make(chan chan<- int),
	}
	go nodeNumber.handleNodeNumber()
	return nodeNumber
}

func (node *ValidatorGoroutine) handleNodeNumber() {
	for {
		select {
		case <-node.ToClose:
			node.StopLoopRoutine()
			return
		case canal := <-node.inGetInt:
			canal <- node.nodeNumber
		}
	}
}
