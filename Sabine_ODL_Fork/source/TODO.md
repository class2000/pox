# Métriques à développer
- [X] Latency
- [ ] Throughput
- [ ] Taux d'échec de la chaîne

# Améliorations
- [ ] Envoyer les transactions par paquets en broadcast
- [ ] Detecter DeadLock et envoyer RoundChange
- [X] Ajout de délai réseaux
- [X] Généraliser si arrivé message dans désordre -> construction chaîne
- [X] Passer en automate à état
    - [ ] Enlever les IsActiceValidator lors de la reception des messages
- [ ] Faire des blocs de taille maximal