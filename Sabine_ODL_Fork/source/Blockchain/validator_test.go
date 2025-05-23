package Blockchain

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"sort"
	"testing"
)

func TestValidators_SetNumberOfNode(t *testing.T) {
	type args struct {
		NumberOfNodes int
		NumberOfVal   int
	}
	tests := []struct {
		name   string
		fields args
	}{
		{"All nodes are validators", args{150, 150}},
		{"All nodes are validators", args{150, 25}},
		{"All nodes are validators", args{150, 100}},
		{"All nodes are validators", args{150, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var validator Validators
			validator.GenerateAddresses(tt.fields.NumberOfNodes)
			validator.SetNumberOfNode(tt.fields.NumberOfVal)
			for i := 0; i < tt.fields.NumberOfNodes; i++ {
				wallet := NewWallet(fmt.Sprintf("NODE%d", i))
				if got := validator.IsValidator(wallet.PublicKey()); !got {
					t.Errorf("Validator %d is validator? %v, want %v", i, got, true)
				}
				if got := validator.IsActiveValidator(wallet.PublicKey()); got != (i < tt.fields.NumberOfVal) {
					t.Errorf("Validator %d is validator? %v, want %v", i, got, i < tt.fields.NumberOfVal)
				}
			}

		})
	}
}

func TestValidators_SetNewListOfValidator(t *testing.T) {
	type args struct {
		NumberOfNodes int
		NumberOfVal   int
		SuggestedList []int
	}
	tests := []struct {
		name   string
		fields args
	}{
		{"Short list of Validator, controled choice", args{20, 5, []int{3, 4, 5, 8}}},
		{"All nodes are validators, Random selection", args{150, 150, []int{}}},
		{"All nodes are validators, random selection", args{150, 4, []int{}}},
		{"All nodes are validators, random selection", args{150, 75, []int{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var validator Validators
			validator.GenerateAddresses(tt.fields.NumberOfNodes)

			nbVal := tt.fields.NumberOfVal
			var listValIndex []int

			if len(tt.fields.SuggestedList) == 0 {
				listValIndex = generateRandomList(tt.fields.NumberOfVal, tt.fields.NumberOfNodes, -1)
			} else {
				listValIndex = tt.fields.SuggestedList
				nbVal = len(listValIndex)
			}

			listNewValue := make([]ed25519.PublicKey, nbVal)
			for i := 0; i < nbVal; i++ {
				wallet := NewWallet(fmt.Sprintf("NODE%d", listValIndex[i]))
				listNewValue[i] = wallet.PublicKey()
			}

			validator.SetNewListOfValidator(listNewValue)

			lenList := len(validator.GetValidatorList())

			if lenList != nbVal {
				t.Errorf("Expected a number of validator of %d, got %d", nbVal, lenList)
			}

			for i := 0; i < nbVal; i++ {
				valId := listValIndex[i]
				wallet := NewWallet(fmt.Sprintf("NODE%d", valId))
				if !validator.IsActiveValidator(wallet.PublicKey()) {
					t.Errorf("The validator %d is supposed to be a validator", valId)
				}
			}

			for i := 0; i < tt.fields.NumberOfNodes; i++ {
				wallet := NewWallet(fmt.Sprintf("NODE%d", i))
				expected := containsInt(listValIndex, i)
				if got := validator.IsActiveValidator(wallet.PublicKey()); got != expected {
					t.Errorf("Validator %d is validator? %v, want %v", i, got, expected)
				}
			}

			listValIndexSorted := make([]int, nbVal)
			copy(listValIndexSorted, listValIndex)
			sort.Ints(listValIndexSorted)
			listNewValueSorted := make([]ed25519.PublicKey, nbVal)
			for i := 0; i < nbVal; i++ {
				wallet := NewWallet(fmt.Sprintf("NODE%d", listValIndexSorted[i]))
				listNewValueSorted[i] = wallet.PublicKey()
			}
			listNewValidators := validator.GetValidatorList()

			for i, newValidator := range listNewValueSorted {
				if !bytes.Equal(listNewValidators[i], newValidator) {
					t.Errorf("The validator %d is supposed to be a validator", listValIndex[i])
				}
			}
		})
	}
}

func containsInt(s []int, e int) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
