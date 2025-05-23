#!/usr/bin/env python3
import sys

if not (len(sys.argv) == 5):
    print("not Enough argument")
    print("python calcDebit.py [nb_node] [max_val_capa] [min_val_capa] [nb_val]")
    exit(1)

nb_node = float(sys.argv[1])
max_val_capa = float(sys.argv[2])
min_val_capa = float(sys.argv[3])
nb_val = float(sys.argv[4])
securite = 12

if __name__ == '__main__':
    alpha = (max_val_capa - min_val_capa) / (nb_node - 4)
    beta = -4 * alpha + min_val_capa
    print(int(alpha * nb_val + beta) + securite)
