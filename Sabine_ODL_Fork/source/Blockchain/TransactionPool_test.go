package Blockchain

import (
	"fmt"
	"pbftnode/source/config"
	"testing"
	"time"
)

func TestTransactionPool_GetTxForBloc(t *testing.T) {
	tests := []struct {
		name   string
		nbOfTx int
	}{
		{"No tx", 0},
		{"Less than a block", 3},
		{"More than a block", config.BlockSize + 1},
		{"A lot more", 10*config.BlockSize + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wallet := NewWallet("NODE0")
			var validator ValidatorInterf = &Validators{}
			validator.GenerateAddresses(16)
			transactionPool := NewTransactionPool(validator, 0)
			transactionPool.SetMetricHandler(whiteMetric{})
			for i := 0; i < tt.nbOfTx; i++ {
				transac := wallet.CreateBruteTransaction([]byte(fmt.Sprintf("Transaction number n°%d", i)))
				time.Sleep(20 * time.Millisecond)
				transactionPool.AddTransaction(*transac)
			}
			time.Sleep(50 * time.Millisecond)
			order := wallet.CreateTransaction(Commande{
				Order:           VarieValid,
				Variation:       16,
				NewValidatorSet: validator.GetNodeList(),
			})
			resp := transactionPool.AddTransaction(*order)

			if !resp {
				t.Error("The transaction haven't be added in the pool")
			}

			txList := transactionPool.GetTxForBloc()
			var present bool
			for _, tx := range txList {
				if tx.GetHashPayload() == order.GetHashPayload() {
					present = true
					break
				}
			}
			if !present {
				t.Error("The order is not in the proposed block")
			}
		})
	}
}

type whiteMetric struct{}

func (white whiteMetric) AddNCommittedTx(int) {}

func (white whiteMetric) SetHTTPViewer(string) {}

func (white whiteMetric) GetThroughputs() (float64, float64, float64, float64) { return 0, 0, 0, 0 }

func (white whiteMetric) AddOneCommittedTx() {}

func (white whiteMetric) AddOneReceivedTx() {}

func (white whiteMetric) AddOneCommittedBlock() {}

func (white whiteMetric) GetMetrics() Metrics { return Metrics{} }

func TestSortedStruct(t *testing.T) {
	global := make(map[string]Transaction)
	global["aaa"] = Transaction{TransaCore: transactCore{Timestamp: 5}}
	global["bbb"] = Transaction{TransaCore: transactCore{Timestamp: 10}}
	global["ccc"] = Transaction{TransaCore: transactCore{Timestamp: 15}}
	global["ddd"] = Transaction{TransaCore: transactCore{Timestamp: 20}}
	global["eee"] = Transaction{TransaCore: transactCore{Timestamp: 25}}
	global["fff"] = Transaction{TransaCore: transactCore{Timestamp: 30}}

	tests := []struct {
		name      string
		sortedStr sortedStruct
		insertion []string
		size      int
		expected  []string
	}{
		{"Sorted List", newSortedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"}, 5, []string{"aaa", "bbb", "ccc", "ddd", "eee"}},
		{"Linked List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"}, 5, []string{"aaa", "bbb", "ccc", "ddd", "eee"}},

		{"Sorted List", newSortedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"}, 3, []string{"aaa", "bbb", "ccc"}},
		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"}, 3, []string{"aaa", "bbb", "ccc"}},

		{"Sorted List", newSortedList(&global), []string{"aaa", "ddd", "bbb"}, 5, []string{"aaa", "bbb", "ddd"}},
		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb"}, 5, []string{"aaa", "bbb", "ddd"}},

		{"Sorted List", newSortedLinkedList(&global), []string{"bbb", "ddd", "aaa"}, 5, []string{"aaa", "bbb", "ddd"}},

		{"Sorted List", newSortedLinkedList(&global), []string{"ccc", "aaa", "bbb"}, 5, []string{"aaa", "bbb", "ccc"}},
		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "bbb", "eee", "ddd", "ccc"}, 5, []string{"aaa", "bbb", "ccc", "ddd", "eee"}},
		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "bbb", "eee", "ccc", "ddd"}, 5, []string{"aaa", "bbb", "ccc", "ddd", "eee"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, hash := range tt.insertion {
				tt.sortedStr.insert(hash)
			}
			solution := tt.sortedStr.getFirstsElem(tt.size)
			if len(tt.expected) != len(solution) {
				t.Errorf("Expected a size of %d, get %d", len(tt.expected), len(solution))
			}
			var rightOrder bool = true
			for i := 0; i < len(tt.expected); i++ {
				if tt.expected[i] != solution[i] {
					rightOrder = false
				}
			}
			if !rightOrder {
				t.Error("Mauvais ordre")
				for i := 0; i < len(tt.expected); i++ {
					fmt.Printf("Expected %s, get %s\n", tt.expected[i], solution[i])
				}
			}
		})
	}
}

func TestSortedStruct_remove(t *testing.T) {
	global := make(map[string]Transaction)
	global["aaa"] = Transaction{TransaCore: transactCore{Timestamp: 5}}
	global["bbb"] = Transaction{TransaCore: transactCore{Timestamp: 10}}
	global["ccc"] = Transaction{TransaCore: transactCore{Timestamp: 15}}
	global["ddd"] = Transaction{TransaCore: transactCore{Timestamp: 20}}
	global["eee"] = Transaction{TransaCore: transactCore{Timestamp: 25}}
	global["fff"] = Transaction{TransaCore: transactCore{Timestamp: 30}}

	tests := []struct {
		name      string
		sortedStr sortedStruct
		insertion []string
		removal   []string
		size      int
		expected  []string
	}{
		{"Sorted List", newSortedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"},
			[]string{"aaa", "ddd"}, 5, []string{"bbb", "ccc", "eee"}},
		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"},
			[]string{"aaa", "ddd"}, 5, []string{"bbb", "ccc", "eee"}},

		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"},
			[]string{"aaa", "eee"}, 5, []string{"bbb", "ccc", "ddd"}},

		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"},
			[]string{"aaa", "bbb", "ccc"}, 5, []string{"ddd", "eee"}},

		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"},
			[]string{"eee", "ddd"}, 5, []string{"aaa", "bbb", "ccc"}},
		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"},
			[]string{"ccc", "bbb"}, 5, []string{"aaa", "ddd", "eee"}},

		{"Sorted List", newSortedLinkedList(&global), []string{"aaa", "ddd", "bbb", "ccc", "eee"},
			[]string{"aaa", "ddd", "bbb", "ccc", "eee"}, 5, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, hash := range tt.insertion {
				tt.sortedStr.insert(hash)
			}
			for _, hash := range tt.removal {
				tt.sortedStr.remove(hash)
			}
			solution := tt.sortedStr.getFirstsElem(tt.size)
			if len(tt.expected) != len(solution) {
				for i, hash := range solution {
					fmt.Printf("%d\t%s\n", i, hash)
				}
				t.Errorf("Expected a size of %d, get %d", len(tt.expected), len(solution))
			}
		})
	}
}
