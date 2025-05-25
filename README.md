# PBFT-Enhanced SDN Control Plane using POX and Go (Sabine Fork Adaptation)

This project implements and evaluates a distributed Software-Defined Networking (SDN) control plane. It uses a Practical Byzantine Fault Tolerance (PBFT) consensus mechanism, built in Go (as an adaptation of the Sabine project), to achieve agreement on network control decisions. This system is integrated with the Python-based POX SDN controller framework, where POX instances act as a primary controller and multiple deterministic replicas.

The core objective is to ensure that critical SDN forwarding decisions (e.g., PacketOut actions resulting from PacketIn events) are validated by a distributed set of replicas and agreed upon via PBFT consensus *before* these actions are applied to the network switches. This architecture aims to enhance consistency and resilience in distributed SDN environments.

## Key Features & Overview

*   **Consensus-Driven SDN Control:** SDN actions proposed by a POX Primary controller are not immediately executed. Instead, they are submitted as transactions to a Go-based PBFT network.
*   **Externalized Deterministic Validation:** PBFT nodes, during the consensus process, query dedicated POX Replica instances via gRPC. These replicas independently compute the expected SDN action based on the packet information, providing a validation layer for the primary's proposal.
*   **Go PBFT System (in `Sabine_ODL_Fork/`):**
    *   Adapted from Dr. Guilain Leduc's "Sabine" project ([https://github.com/inpprenable/Sabine](https://github.com/inpprenable/Sabine)).
    *   **PBFT Nodes (`pbftnode node`):** Execute the PBFT protocol.
    *   **PBFT Dealer (`pbftnode dealer`):** Acts as a gRPC gateway for the POX Primary and relays consensus results.
    *   **PBFT Bootstrap (`pbftnode bootstrap`):** Facilitates peer discovery.
*   **POX SDN Controller System (in `ext/pbft_pox_app.py` and standard POX files):**
    *   **POX Primary:** Manages switch connections, proposes SDN actions for consensus.
    *   **POX Replicas:** Provide deterministic action computation for PBFT node validation.
*   **Communication:**
    *   gRPC (Protocol Buffers in `Sabine_ODL_Fork/proto/`) for POX $\leftrightarrow$ Go PBFT system interactions.
    *   Custom TCP + Gob for intra-Go PBFT network P2P communication and result reporting.
*   **Deployment:** Containerized using Docker and orchestrated with a dynamically generated `docker-compose.yml` file via `generate_docker_compose.py`.
*   **Network Emulation:** Typically uses Mininet to connect virtual switches to the POX Primary.

## Architecture and Workflow

The typical flow for a `PacketIn` event is:

1.  **PacketIn:** Switch sends `PacketIn` to **POX Primary**.
2.  **Proposal:** POX Primary computes a proposed action (e.g., flood, packet_out).
3.  **Consensus Request:** POX Primary sends a gRPC `ConsensusRequest` to **PBFT Dealer**.
4.  **PBFT Transaction:** Dealer creates an SDN transaction and broadcasts it to **PBFT Nodes**.
5.  **PBFT Round:**
    *   The PBFT proposer initiates a Pre-Prepare.
    *   Validator nodes receive Pre-Prepare, make gRPC calls to their **POX Replicas** for action validation.
    *   If validation succeeds, nodes exchange Prepare and Commit messages.
6.  **Block Commit & Result:** Nodes finalize the block. The committing node(s) send a `ConsensusResultMess` via TCP to the **PBFT Dealer's** income port (default 5000).
7.  **Response to Primary:** Dealer sends a gRPC `ConsensusResponse` to **POX Primary**.
8.  **Action Execution:** POX Primary sends the final OpenFlow command to the switch.

## Prerequisites

*   **Git**
*   **Docker Engine**
*   **Docker Compose** (V2 or later recommended)
*   **Python 3** (for `generate_docker_compose.py` and POX)
*   **(Optional) Go (e.g., 1.23+):** If modifying `Sabine_ODL_Fork`.
*   **(Optional) Mininet:** For running OpenFlow switches. The `pmanzoni/mininet-in-a-container` Docker image is recommended.

## Setup and Installation

1.  **Clone this Repository (which includes POX and your Sabine fork):**
    ```bash
    git clone https://github.com/class2000/pox.git
    cd pox
    ```

## Running the Simulation

The following steps outline a typical experiment run:

1.  **Generate Docker Compose Configuration:**
    From the root of this repository (the `pox/` directory after cloning):
    ```bash
    python3 generate_docker_compose.py <number_of_pbft_nodes>
    ```
    For example, for 4 PBFT nodes (which means 1 POX Primary, 4 POX Replicas, 1 Dealer, 1 Bootstrap, and 4 PBFT Nodes):
    ```bash
    python3 generate_docker_compose.py 4
    ```
    This creates `docker-compose.generated.yml`.


2.  **Start PBFT/POX Services:**
    Use the generated (or main) Docker Compose file:
    ```bash
    docker compose -f docker-compose.generated.yml up --build -d
    ```

    This will build images if necessary and start all services in detached mode. The POX Primary will listen on port 6633 (mapped to host).

3.  **Run Mininet Container:**
    Open a new terminal for Mininet. The following command runs Mininet in a container using the host's network, which simplifies connecting to the POX controller.
    ```bash
    docker run -it --rm --privileged --network=host \
      -e DISPLAY \
      -v /tmp/.X11-unix:/tmp/.X11-unix \
      -v /lib/modules:/lib/modules \
      --name mininet pmanzoni/mininet-in-a-container:amd64
    ```
    *For ARM64 (e.g., Mac M-series), XQuartz setup might be needed for GUI, or run headless.*

4.  **Start Mininet Topology and Connect to POX Primary:**
    Inside the Mininet container's shell:
    ```bash
    mn --controller=remote,ip=127.0.0.1,port=6633 --switch=ovsk,protocols=OpenFlow10 --topo=single,2
    ```
    This creates a topology with one switch (s1) and two hosts (h1, h2), connecting to the POX controller running on the host machine (accessible via 127.0.0.1 due to `--network=host`).

5.  **Perform Tests:**
    Inside the Mininet CLI (`mininet>`):
    ```bash
    # Example: Send 100 ICMP Echo Requests from h1 to h2, with a 10-second interval between each ping.
    # This test will take approximately 1000 seconds (over 16 minutes) to complete.
    h1 ping -i 10 -c 100 h2
    ```
    While the test is running, you can monitor the logs of the Docker containers:
    ```bash
    docker-compose -f docker-compose.generated.yml logs -f pox-primary pbft-dealer pbft-node-0 # and other nodes
    # Or if using the static docker-compose.yml:
    # docker-compose logs -f pox-primary pbft-dealer pbft-node-0
    ```

6.  **Analyze Results:**
    *   Ping RTTs will be displayed by the `ping` command in Mininet.
    *   Controller-side latencies (e.g., "Dealer Response Latency") can be extracted from `pox-primary` logs using a parser script (see `test_runs/pox_primary_log_parser.py` for an example).
    *   PBFT transaction throughput can be obtained from the Go nodes' metrics endpoints or saved files if configured.

7.  **Stopping the Simulation:**
    To stop and remove the PBFT/POX services:
    ```bash
    # If using a generated file:
    docker compose -f docker-compose.generated.yml down
    # If using the static docker-compose.yml:
    # docker compose down
    ```
    To exit Mininet, type `exit` in its CLI. The Mininet container will be removed automatically due to `--rm`.

## Current Status & Future Work

*   **Core Functionality:** The system successfully demonstrates end-to-end consensus on SDN PacketOut actions. PBFT nodes correctly validate proposals against deterministic POX Replicas. Proposer rotation is functional.
*   **Performance Baseline:** Initial latency tests (e.g., "Dealer Response Latency" around 15-25ms for 4 nodes) indicate efficient core operations.
*   **Areas for Development:**
    *   **Full PBFT View Change Protocol:** To robustly handle faulty PBFT primary nodes.
    *   **Strict Mismatch Handling:** Option for PBFT nodes to reject PrePrepares if POX Replica validation fails (currently only warns).
    *   **Enhanced Configuration:** Externalize more parameters (e.g., Dealer's result port).
    *   **Comprehensive Resilience Testing:** Systematic fault injection (Byzantine replicas, node crashes).
    *   **Further Performance Profiling and Optimization.**
    *   **Security Hardening (TLS, AuthN/AuthZ).**

## Code Origins and Acknowledgements

The Go-based PBFT consensus system (bootstrap, dealer, and node components, located in the `Sabine_ODL_Fork/` directory) is a fork and adaptation of the "Sabine" project, originally developed by Dr. Guilain Leduc.
*   Original Sabine Repository: [https://github.com/inpprenable/Sabine](https://github.com/inpprenable/Sabine)

This codebase was adapted to integrate with the POX SDN controller and to explore consensus in the context of SDN control plane decisions.
