import sys
import yaml

def generate_compose(n):
    services = {}

    # Define networks
    networks = {
        "pbft_net": {
            "driver": "bridge"
        }
    }

    # Bootstrap service
    services["pbft-bootstrap"] = {
        "build": {"context": "./Sabine_ODL_Fork", "dockerfile": "Dockerfile"},
        "container_name": "pbft-bootstrap",
        "command": ["bootstrap", "4315", "--debug", "info"],
        "networks": ["pbft_net"],
        "ports": ["4315:4315"]
    }

    # --- Corrected Port Logic Start ---
    base_replica_port = 50052
    # --- Corrected Port Logic End ---

    # Dealer service
    replica_addrs = [f"pox-replica-{i}:{base_replica_port+i}" for i in range(n)]
    services["pbft-dealer"] = {
        "build": {"context": "./Sabine_ODL_Fork", "dockerfile": "Dockerfile"},
        "container_name": "pbft-dealer",
        "command": [
            "dealer", "pbft-bootstrap:4315", "5000",
            "--grpcPort", "50051",
            "--NbNode", str(n),
            "--replicaAddrs", ",".join(replica_addrs),
            "--debug", "info"
        ],
        "networks": ["pbft_net"],
        "ports": ["50051:50051", "5000:5000"],
        "depends_on": ["pbft-bootstrap"]
    }

    # PBFT nodes
    for i in range(n):
        services[f"pbft-node-{i}"] = {
            "build": {"context": "./Sabine_ODL_Fork", "dockerfile": "Dockerfile"},
            "container_name": f"pbft-node-{i}",
            "command": [
                "node", "pbft-bootstrap:4315", str(i),
                "--NodeNumber", str(n),
                "--ryuReplicaAddr", f"pox-replica-{i}:{base_replica_port+i}",
                "--acceptUnknownTx",
                "--debug", "trace"
            ],
            "networks": ["pbft_net"],
            "depends_on": ["pbft-bootstrap"]
        }

    # POX primary controller
    services["pox-primary"] = {
        "build": {"context": ".", "dockerfile": "Dockerfile"},
        "container_name": "pox-primary",
        "command": [
            "./pox.py", "log.level", "--DEBUG", "pbft_pox_app",
            "--primary=true",
            "--dealer_ip=pbft-dealer",
            "--dealer_port=50051",
            "openflow.discovery",
            "--eat_early_packets=false"
        ],
        "networks": ["pbft_net"],
        "ports": ["6633:6633"],
        "depends_on": ["pbft-dealer"]
    }

    # POX replicas
    for i in range(n):
        services[f"pox-replica-{i}"] = {
            "build": {"context": ".", "dockerfile": "Dockerfile"},
            "container_name": f"pox-replica-{i}",
            "command": [
                "./pox.py", "log.level", "--DEBUG", "pbft_pox_app",
                "--primary=false",
                f"--replica_id={i}"
            ],
            "networks": ["pbft_net"],
            "ports": [f"{base_replica_port+i}:{base_replica_port+i}"]
        }

    compose_dict = {
        "version": "3.8",
        "networks": networks,
        "services": services
    }

    with open("docker-compose.generated.yml", "w") as f:
        yaml.dump(compose_dict, f, sort_keys=False, default_flow_style=False)

    print(f"Generated docker-compose.generated.yml for {n} PBFT nodes and {n} POX replicas.")

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: python generate_docker_compose.py <number_of_nodes>")
    else:
        generate_compose(int(sys.argv[1]))