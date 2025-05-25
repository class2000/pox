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

5.  **Perform Network Tests (e.g., Ping):**
    Once Mininet is running and its switches are connected to the `pox-primary` controller, you can execute network tests from the Mininet CLI (`mininet>`).

    *   **Example: ICMP Ping Test**
        To send 100 ICMP Echo Requests from host `h1` to `h2`, with a 10-second interval between each ping (this long interval helps in observing individual consensus rounds if needed, but can be adjusted):
        ```bash
        mininet> h1 ping -i 10 -c 100 h2 > /path_to_repo/test_runs/<test_category>/<run_id>/300.txt
        ```
        **Explanation:**
        *   `h1 ping -i 10 -c 100 h2`: Instructs host `h1` to ping `h2`, sending 100 packets (`-c 100`) with a 10-second interval (`-i 10`).
        *   `> /path_to_repo/test_runs/<test_category>/<run_id>/300.txt`: This redirects the output of the `ping` command (which includes RTTs) to a file.
            *   Replace `/path_to_repo/` with the actual path on your system where you cloned the `class2000/pox` repository.
            *   `<test_category>`: A directory representing the configuration being tested (e.g., `4nodes`, `8nodes`).
            *   `<run_id>`: A subdirectory for a specific run of this configuration (e.g., `run1`, `run2`).
            *   `300.txt`: The filename expected by the `plot_roundtrip.py` analysis script.
        *   **Important:** Ensure these directories exist before running the `ping` command with output redirection. For example:
            ```bash
            # On your host machine, before running ping in Mininet:
            mkdir -p test_runs/4nodes/run1
            ```
            Then, in Mininet (adjust path if Mininet is in a container without direct host FS access, though `--network=host` often simplifies this, or use Docker volumes):
            ```bash
            mininet> h1 ping -c 100 h2 > ./test_runs/4nodes/run1/300.txt 
            # (Assuming 'test_runs' is accessible from Mininet's CWD, or provide full path)
            ```

    *   **Monitoring Logs:**
        While tests are running, you can monitor the logs of the Docker containers in a separate host terminal:
        ```bash
        docker compose -f docker-compose.yml logs -f pox-primary pbft-dealer pbft-node-0 pbft-node-1 # ... and other relevant services
        ```
        *(Adjust `-f docker-compose.yml` if you are using `docker-compose.generated.yml` for this specific run).*

6.  **Analyze Results (After Test Completion):**
    The primary analysis for ping RTTs is performed using the `test_runs/plot_roundtrip.py` script.

    *   **Data Organization for `plot_roundtrip.py`:**
        Ensure your ping output files (`300.txt`) are organized as follows within your `test_runs` directory:
        ```
        test_runs/
        ├── 4nodes/  # Category for tests with 4 PBFT nodes
        │   ├── run1/
        │   │   └── 300.txt
        │   └── run2/
        │       └── 300.txt
        ├── 8nodes/  # Category for tests with 8 PBFT nodes
        │   ├── run1/
        │   │   └── 300.txt
        │   └── run2/
        │       └── 300.txt
        ├── ... (other categories for different node counts)
        └── plot_roundtrip.py
        ```
    *   **Run the Analysis Script:**
        Navigate to your `test_runs/` directory (or ensure the `base_path` variable within `plot_roundtrip.py` points to this directory) and execute:
        ```bash
        python3 plot_roundtrip.py
        ```
        The script will process the `300.txt` files, aggregate RTT data, print summary statistics (min, max, mean, median, std dev, percentiles, SLO compliance, jitter, etc.), perform ANOVA and Tukey HSD tests, and generate various plots visualizing the RTT distributions and trends across different categories (node counts).

    *   **Analyzing Controller-Side and PBFT Metrics:**
        *   **Dealer Response Latency:** As measured by the POX Primary (e.g., `INFO:PBFT_POX:Primary Thread ... Received Response from Dealer in Xs. Consensus=True`). These can be extracted from `pox-primary` logs using a separate parser script (e.g., `test_runs/pox_primary_log_parser.py` if you create/adapt one based on previous discussions).
        *   **PBFT Transaction Throughput:** Can be obtained from the Go PBFT nodes by:
            *   Scraping their HTTP metrics endpoint (if configured with `--httpMetric` in `docker-compose.yml`).
            *   Parsing the JSON files saved periodically (if configured with `--metricSaveFile` and `--metricTicker`).
        *   **Qualitative Log Analysis:** Review detailed logs from all components to understand behavior during specific events or to diagnose issues.

7.  **Stopping the Simulation:**
    *   To stop and remove the PBFT/POX services defined in your Docker Compose file:
        ```bash
        docker compose -f docker-compose.yml down
        ```
        *(Replace `docker-compose.yml` with `docker-compose.generated.yml` if that's what you used to start the services for a particular experiment).*
    *   To exit Mininet: Type `exit` in its CLI. If the Mininet Docker container was started with `--rm`, it will be removed automatically.

## Current Status & Future Work

*   **Core Functionality:** The system successfully demonstrates end-to-end consensus on SDN PacketOut actions. PBFT nodes correctly validate proposals against deterministic POX Replicas, and this validation step is integrated into the PBFT consensus flow. Proposer rotation based on the last block hash is functional. The result reporting mechanism from PBFT nodes to the Dealer is operational, allowing the POX Primary to receive final consensus outcomes.
*   **Performance Baseline Established:** Initial latency tests using ping RTTs and analysis of POX Primary logs (for "Dealer Response Latency") show promising performance. For instance, with 4 PBFT nodes, the mean "Dealer Response Latency" (encompassing PBFT consensus, replica calls, and gRPC overhead) has been observed to be around 15-25ms for individual control decisions. The `plot_roundtrip.py` script provides a robust way to analyze end-to-end ping RTTs across different node configurations.
*   **Areas for Development & Further Evaluation:**
    *   **Full PBFT View Change Protocol:** A critical area for future work is the complete implementation and testing of the PBFT view change protocol to robustly handle faulty or non-responsive PBFT primary (proposer) nodes. This involves timeout mechanisms in the `NewRoundSt` state and full logic for `RoundChangeSt` to process `VIEW-CHANGE` and `NEW-VIEW` messages.
    *   **Strict Mismatch Handling & Resilience:**
        *   Currently, a mismatch between the POX Primary's proposed action and a POX Replica's computed action only logs a warning in the PBFT nodes. For enhanced security against a potentially compromised POX Primary or faulty/malicious POX Replicas, this could be configured to cause the PBFT node to reject the PrePrepare message, thus preventing the block from being prepared.
        *   Systematically test the system's resilience by injecting faults:
            *   Simulate POX Replica misbehavior (returning different actions).
            *   Simulate PBFT node crashes (validators and proposers).
            *   Evaluate behavior under various simulated network delays between PBFT nodes using the existing `--delayType` and `--avgDelay` flags.
    *   **Comprehensive Throughput Analysis:** Conduct thorough throughput tests using `pbftnode zombie throughput` to determine the saturation point of the PBFT network and the SDN control decision processing rate under varying loads and node counts. Correlate this with resource utilization (CPU, memory) of all components.
    *   **Enhanced Configuration:** Externalize more parameters for easier experimentation, such as the Dealer's result reporting address used by PBFT nodes.
    *   **Security Hardening:** Consider implementing TLS/SSL for gRPC and P2P channels, and add authentication/authorization layers for inter-component communication.
    *   **Broader State Synchronization:** Explore extending the consensus mechanism to cover the synchronization of more general SDN state (e.g., global topology views, distributed policy databases) beyond individual packet forwarding decisions.
    *   **Optimization:** Based on detailed performance profiling from the extended evaluations, identify and implement potential optimizations in message handling, serialization, or protocol interactions.

## Code Origins and Acknowledgements

The Go-based PBFT consensus system (bootstrap, dealer, and node components, located in the `Sabine_ODL_Fork/` directory) is a fork and adaptation of the "Sabine" project, originally developed by Dr. Guilain Leduc.
*   Original Sabine Repository: [https://github.com/inpprenable/Sabine](https://github.com/inpprenable/Sabine)

This codebase was adapted to integrate with the POX SDN controller and to explore consensus in the context of SDN control plane decisions, particularly by enabling PBFT nodes to consult external POX Replicas for deterministic action validation.
