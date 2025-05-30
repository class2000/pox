# PBFT-Enhanced SDN Control Plane using POX and Go (Sabine Fork Adaptation)

This project implements and evaluates a distributed Software-Defined Networking (SDN) control plane. It uses a Practical Byzantine Fault Tolerance (PBFT) consensus mechanism, built in Go (as an adaptation of the Sabine project), to achieve agreement on network control decisions and network topology state. This system is integrated with the Python-based POX SDN controller framework, where POX instances act as a primary controller and multiple deterministic replicas.

The core objectives are:
1.  To ensure that critical SDN forwarding decisions (e.g., PacketOut actions resulting from PacketIn events) are validated by a distributed set of replicas and agreed upon via PBFT consensus *before* these actions are applied to the network switches.
2.  To maintain a consistent and fault-tolerant view of the network topology (link status) across all controller replicas, with topology changes also being subject to PBFT consensus.

This architecture aims to enhance consistency, resilience, and shared situational awareness in distributed SDN environments.

## Key Features & Overview

*   **Consensus-Driven SDN Control:** SDN actions proposed by a POX Primary controller are not immediately executed. Instead, they are submitted as transactions to a Go-based PBFT network.
*   **Consensus-Driven Topology Management:** Link discovery events (up/down) from the POX Primary are also submitted as transactions to the PBFT network. This ensures all POX Replicas build their network view based on a consensus-agreed sequence of topology changes.
*   **Externalized Deterministic Validation:** For packet forwarding decisions, PBFT nodes, during the consensus process, query dedicated POX Replica instances via gRPC. These replicas independently compute the expected SDN action based on the packet information and their consistent topology view, providing a validation layer for the primary's proposal.
*   **Go PBFT System (in `Sabine_ODL_Fork/`):**
    *   Adapted from Dr. Guilain Leduc's "Sabine" project ([https://github.com/inpprenable/Sabine](https://github.com/inpprenable/Sabine)).
    *   **PBFT Nodes (`pbftnode node`):** Execute the PBFT protocol.
    *   **PBFT Dealer (`pbftnode dealer`):** Acts as a gRPC gateway for the POX Primary (for both action consensus requests and link event reports) and relays consensus results.
    *   **PBFT Bootstrap (`pbftnode bootstrap`):** Facilitates peer discovery.
*   **POX SDN Controller System (in `ext/pbft_pox_app.py` and standard POX files):**
    *   **POX Primary:** Manages switch connections, proposes SDN actions for consensus, and reports link discovery events.
    *   **POX Replicas:** Provide deterministic action computation for PBFT node validation and maintain a consistent topology view based on notifications from PBFT nodes.
*   **Communication:**
    *   gRPC (Protocol Buffers in `Sabine_ODL_Fork/proto/`) for POX $\leftrightarrow$ Go PBFT system interactions (action consensus and topology).
    *   Custom TCP + Gob for intra-Go PBFT network P2P communication and result reporting.
*   **Deployment:** Containerized using Docker and orchestrated with a dynamically generated `docker-compose.yml` file via `generate_docker_compose.py`.
*   **Network Emulation:** Typically uses Mininet to connect virtual switches to the POX Primary.
*   **Blockchain Visualization:** PBFT nodes can expose an HTTP endpoint (`--httpChain`) to view the committed blockchain data, including `SdnControlInput` and `LinkEventInput` transactions.
*   **Real-time UI:** A Streamlit application (`real_time_ui.py`) can connect to a PBFT node's HTTP endpoint to visualize the consensus-agreed switch topology derived from committed `LinkEventInput` transactions.

## Architecture and Workflow

The system handles two main types of events via consensus:

**A. Packet Processing (PacketIn Event):**

1.  **PacketIn:** Switch sends `PacketIn` to **POX Primary**.
2.  **Proposal:** POX Primary computes a proposed action (e.g., flood, packet_out).
3.  **Consensus Request:** POX Primary sends a gRPC `ConsensusRequest` to **PBFT Dealer**.
4.  **PBFT Transaction:** Dealer creates an `SdnControlInput` transaction and broadcasts it to **PBFT Nodes**.
5.  **PBFT Round:**
    *   The PBFT proposer initiates a Pre-Prepare with the transaction.
    *   Validator nodes receive Pre-Prepare, make gRPC `CalculateAction` calls to their **POX Replicas** for action validation (replicas use their consistent topology view and deterministic logic).
    *   If validation (comparison of proposed vs. replica-computed action) is acceptable (currently logs warning on mismatch), nodes exchange Prepare and Commit messages.
6.  **Block Commit & Result:** Nodes finalize the block. The committing node(s) send a `ConsensusResultMess` via TCP to the **PBFT Dealer's** income port (default 5000).
7.  **Response to Primary:** Dealer sends a gRPC `ConsensusResponse` to **POX Primary**.
8.  **Action Execution:** POX Primary sends the final OpenFlow command to the switch.

**B. Topology Update (Link Event):**

1.  **Link Discovered:** **POX Primary's** `openflow.discovery` module detects a link up/down event.
2.  **Report Link Event:** POX Primary sends a gRPC `ReportLinkEvent` to **PBFT Dealer**.
3.  **PBFT Transaction:** Dealer creates a `LinkEventInput` transaction and broadcasts it to **PBFT Nodes**.
4.  **PBFT Consensus:** PBFT Nodes achieve consensus on this `LinkEventInput` transaction, committing it to a block.
5.  **Replica Notification:** Upon committing the block, each **PBFT Node** extracts the `LinkEventInfo` and sends a gRPC `NotifyLinkEvent` to its assigned **POX Replica**.
6.  **Replica Updates View:** Each **POX Replica** updates its internal `DeterministicL2SwitchLogic.topology_active_links` based on the received, consensus-agreed link status. This ensures all replicas maintain a consistent view of the network fabric.

## Prerequisites

*   **Git**
*   **Docker Engine**
*   **Docker Compose** (V2 or later recommended)
*   **Python 3** (for `generate_docker_compose.py`, POX, and `real_time_ui.py`)
    *   Python packages: `streamlit`, `requests`, `pandas`, `streamlit-agraph`, `pyyaml` (install via `pip install -r requirements.txt` if you create one).
*   **(Optional) Go (e.g., 1.23+):** If modifying `Sabine_ODL_Fork`.
*   **(Optional) Mininet:** For running OpenFlow switches. The `pmanzoni/mininet-in-a-container` Docker image is recommended.
*   **(Optional) PlantUML Renderer:** To view the PlantUML architecture diagrams.

## Setup and Installation

1.  **Clone this Repository (which includes POX and your Sabine fork):**
    ```bash
    git clone https://github.com/class2000/pox.git
    cd pox
    ```
2.  **(Optional) Create and activate a Python virtual environment:**
    ```bash
    python3 -m venv venv
    source venv/bin/activate 
    pip install streamlit requests pandas streamlit-agraph pyyaml # Or use a requirements.txt
    ```

## Running the Simulation

The following steps outline a typical experiment run:

1.  **Generate Docker Compose Configuration:**
    From the root of this repository (the `pox/` directory after cloning):
    ```bash
    python3 generate_docker_compose.py <number_of_pbft_nodes>
    ```
    For example, for 4 PBFT nodes (which implies 1 POX Primary, 4 POX Replicas, 1 Dealer, 1 Bootstrap, and 4 PBFT Nodes):
    ```bash
    python3 generate_docker_compose.py 4
    ```
    This creates `docker-compose.generated.yml`. Ensure `pbft-node-0` (or another node) in this generated file has the `--httpChain` flag and port mapping if you intend to use the Streamlit UI.

2.  **Start PBFT/POX Services:**
    Use the generated Docker Compose file:
    ```bash
    docker compose -f docker-compose.generated.yml up --build -d
    ```
    This will build images if necessary and start all services in detached mode. The POX Primary will listen on port 6633. `pbft-node-0` (if configured) will listen on its mapped HTTP port (e.g., host 8080).

3.  **Run Mininet Container (Optional, for live network interaction):**
    Open a new terminal for Mininet.
    ```bash
    docker run -it --rm --privileged --network=host \
      -e DISPLAY \
      -v /tmp/.X11-unix:/tmp/.X11-unix \
      -v /lib/modules:/lib/modules \
      --name mininet pmanzoni/mininet-in-a-container:amd64
    ```

4.  **Start Mininet Topology (Inside Mininet container):**
    ```bash
    # Inside the Mininet container's shell:
    mn --controller=remote,ip=127.0.0.1,port=6633 --switch=ovsk,protocols=OpenFlow10 --topo=linear,3 
    # (Example: linear,3 for 3 switches, 2 links. Adjust as needed)
    ```

5.  **Visualize Blockchain Data (Streamlit UI):**
    In a new terminal on your host machine (ensure you're in the `pox/` directory and your Python environment is active if you used one):
    ```bash
    streamlit run real_time_ui.py
    ```
    Open the URL provided by Streamlit in your browser. Configure the "Node IP" (usually `127.0.0.1` or `localhost`) and "Node HTTP Port" (e.g., `8080`, matching what you mapped for `pbft-node-0`). The UI will fetch blockchain data and display the consensus-agreed switch topology.

6.  **Perform Network Tests (e.g., Ping in Mininet):**
    While Mininet is running and connected to the `pox-primary`:
    ```bash
    # Inside Mininet CLI (mininet>)
    mininet> h1 ping -c 5 h2 
    ```
    Observe logs in `pox-primary`, `pbft-dealer`, and `pbft-node-X` containers to see the PacketIn processing flow. The Streamlit UI should remain stable or reflect only link changes, as PacketIn events don't alter the switch fabric topology recorded on the blockchain.

    *   **For RTT analysis (as previously detailed):**
        ```bash
        # On your host machine, create directories:
        mkdir -p test_runs/4nodes_linear3/run1 
        # Inside Mininet container (adjust path if needed):
        mininet> h1 ping -i 10 -c 100 h2 > /path_to_repo_on_host/test_runs/4nodes_linear3/run1/300.txt
        ```

7.  **Analyze Results (After Test Completion):**
    *   Use `test_runs/plot_roundtrip.py` for ping RTT analysis.
    *   Analyze POX Primary logs for Dealer Response Latency.
    *   Inspect blockchain data via the Streamlit UI or the node's direct HTTP endpoint (`http://localhost:8080/`).

8.  **Stopping the Simulation:**
    *   Docker services: `docker compose -f docker-compose.generated.yml down`
    *   Mininet: `exit` in its CLI.
    *   Streamlit UI: Ctrl+C in its terminal.

## Current Status & Future Work

*   **Core Functionality:**
    *   End-to-end consensus on SDN PacketOut actions is functional.
    *   Consensus-driven dissemination of network link topology (up/down events) is implemented, allowing replicas to maintain a consistent view of the switch fabric.
*   **Performance Baseline Established:** Initial latency tests are promising.
*   **Visualization:** Blockchain data can be viewed via a node's HTTP endpoint, and the consensus-agreed switch topology can be visualized in real-time using the Streamlit UI.
*   **Areas for Development & Further Evaluation:**
    *   **Full PBFT View Change Protocol:** Critical for handling faulty PBFT proposers.
    *   **Strict Mismatch Handling (SDN Actions):** Enhance resilience by allowing PBFT nodes to reject blocks if a POX Replica's computed action significantly differs from the POX Primary's proposal.
    *   **Host Discovery via Consensus:** Extend the system to achieve consensus on host locations for a fully BFT network view (currently, Streamlit UI only shows switches/links from blockchain).
    *   **Comprehensive Throughput & Scalability Analysis:** Test with more nodes, varying topologies, and higher transaction rates for both SDN actions and topology updates.
    *   **Enhanced Configuration & Security.**
    *   **Broader State Synchronization.**
    *   **Optimization.**

## Code Origins and Acknowledgements

The Go-based PBFT consensus system (bootstrap, dealer, and node components, located in the `Sabine_ODL_Fork/` directory) is a fork and adaptation of the "Sabine" project, originally developed by Dr. Guilain Leduc.
- **Original Sabine Repository:** [https://github.com/inpprenable/Sabine](https://github.com/inpprenable/Sabine)

This codebase was adapted to integrate with the POX SDN controller and to explore consensus in the context of SDN control plane decisions and network state (topology), particularly by enabling PBFT nodes to consult external POX Replicas for deterministic action validation and by having replicas build topology from consensus-agreed link events.

A valuable resource used during development for containerizing and running Mininet—especially helpful on macOS (both Intel and ARM) to avoid using heavy VMs—is:
- **Mininet-in-a-Container by Paolo Manzoni:** [https://github.com/pmanzoni/mininet-in-a-container](https://github.com/pmanzoni/mininet-in-a-container)