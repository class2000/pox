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

    base_replica_port = 50052

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
        node_command = [
            "node", "pbft-bootstrap:4315", str(i),
            "--NodeNumber", str(n),
            "--ryuReplicaAddr", f"pox-replica-{i}:{base_replica_port+i}",
            "--acceptUnknownTx",
            "--debug", "trace"
        ]
        node_ports = [] # Default to no extra ports

        # Example: Expose httpChain for node 0 on host port 8080
        # The internal port for the node will be, for example, 7000
        if i == 0: # Only for pbft-node-0
            internal_http_chain_port = "7000" # Choose an internal port
            host_http_chain_port = "8080"    # Choose a host port
            node_command.extend(["--httpChain", internal_http_chain_port])
            node_ports.append(f"{host_http_chain_port}:{internal_http_chain_port}")

        services[f"pbft-node-{i}"] = {
            "build": {"context": "./Sabine_ODL_Fork", "dockerfile": "Dockerfile"},
            "container_name": f"pbft-node-{i}",
            "command": node_command,
            "networks": ["pbft_net"],
            "depends_on": ["pbft-bootstrap"],
            # Add ports only if defined for this node
            **({"ports": node_ports} if node_ports else {}) 
        }

    # POX primary controller
    services["pox-primary"] = {
        "build": {"context": ".", "dockerfile": "Dockerfile"},
        "container_name": "pox-primary",
        "command": [
            "./pox.py", "log.level", "--DEBUG",
            "openflow.discovery",           
            "--eat_early_packets=false",    
            "pbft_pox_app",                 
            "--primary=true",
            "--dealer_ip=pbft-dealer",
            "--dealer_port=50051"
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
                "./pox.py", "log.level", "--DEBUG", "pbft_pox_app", # No discovery needed for replicas by default
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

    output_filename = "docker-compose.generated.yml"
    with open(output_filename, "w") as f:
        yaml.dump(compose_dict, f, sort_keys=False, default_flow_style=False)

    print(f"Generated {output_filename} for {n} PBFT nodes and {n} POX replicas.")

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: python generate_docker_compose.py <number_of_nodes>")
        sys.exit(1) # Added exit for incorrect usage
    try:
        num_nodes = int(sys.argv[1])
        if num_nodes <= 0:
            print("Error: Number of nodes must be a positive integer.")
            sys.exit(1)
        generate_compose(num_nodes)
    except ValueError:
        print("Error: Invalid number of nodes. Please provide an integer.")
        sys.exit(1)