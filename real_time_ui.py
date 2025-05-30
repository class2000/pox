import streamlit as st
import requests # Still used for fetching from the Go node's HTTP endpoint
import json
import threading
import time
import pandas as pd
from streamlit_agraph import agraph, Node, Edge, Config
import logging
from requests.exceptions import RequestException, HTTPError, ConnectionError, Timeout, JSONDecodeError
from collections import OrderedDict

# --- Configuration ---
DEFAULT_PBFT_NODE_IP = "127.0.0.1"  # IP of the PBFT node exposing --httpChain
DEFAULT_PBFT_NODE_HTTP_PORT = 8080 # Port mapped from the PBFT node's --httpChain
BLOCKCHAIN_API_PATH = "/"            # Usually the root path for the --httpChain endpoint
API_TIMEOUT = 20 # Increased timeout for potentially larger blockchain data

# --- UI Colors (can remain the same) ---
HOST_COLOR = "#1f77b4"
SWITCH_COLOR = "#ff7f0e"
LINK_COLOR = "#6c757d"
LINK_HIGHLIGHT_COLOR = "#495057"
# HOST_LINK_COLOR = "#17a2b8" # Host links won't be directly from blockchain data in this version
# HOST_LINK_HIGHLIGHT_COLOR = "#138496"

logging.basicConfig(level=logging.INFO, format='%(asctime)s [%(threadName)s] %(levelname)s: %(message)s')

# --- API Fetching Functions (Modified for Blockchain) ---

def _fetch_blockchain_data_http(url, timeout, results_dict, key):
    """Fetches blockchain data from a Go PBFT node's HTTP endpoint."""
    thread_name = threading.current_thread().name
    logging.info(f"Fetching blockchain data from {url}...")
    try:
        response = requests.get(url, timeout=timeout)
        response.raise_for_status()
        # The --httpChain endpoint returns a JSON array of blocks directly
        results_dict[key] = response.json()
        logging.info(f"Success fetching {key} (blockchain data).")
    except HTTPError as e:
        status_code = e.response.status_code if e.response else 500
        error_msg = f"HTTP Error ({status_code})"
        logging.error(f"{error_msg} fetching {key}: {e}")
        results_dict[key] = {"error": error_msg, "status_code": status_code, "data": []} # Ensure data is a list on error
    except Timeout:
        logging.error(f"Timeout error fetching {key}")
        results_dict[key] = {"error": "Timeout", "data": []}
    except ConnectionError:
        logging.error(f"Connection error fetching {key}")
        results_dict[key] = {"error": "Connection Error", "data": []}
    except JSONDecodeError:
        logging.error(f"Error decoding JSON for {key}")
        results_dict[key] = {"error": "JSON Decode Error", "data": []}
    except RequestException as e:
        logging.error(f"Request error fetching {key}: {e}")
        results_dict[key] = {"error": f"Request Error: {e}", "data": []}
    except Exception as e:
        logging.exception(f"Unexpected error fetching {key}")
        results_dict[key] = {"error": f"Unexpected Error: {e}", "data": []}


@st.cache_data(ttl=15) # Shorter TTL for blockchain as it can change more frequently
def fetch_topology_from_blockchain(pbft_node_ip, pbft_node_http_port, timeout=API_TIMEOUT):
    """
    Fetches blockchain data and processes LinkEventInput transactions
    to build the switch topology.
    """
    logging.info(f"--- Starting Blockchain Data Fetch from {pbft_node_ip}:{pbft_node_http_port} ---")
    start_time = time.time()
    blockchain_url = f"http://{pbft_node_ip}:{pbft_node_http_port}{BLOCKCHAIN_API_PATH}"
    results = {} # To store fetched data

    # We only have one data source now: the blockchain
    # Using a thread for consistency, though could be direct for one source
    thread = threading.Thread(
        target=_fetch_blockchain_data_http,
        args=(blockchain_url, timeout, results, "blockchain"),
        name="BlockchainFetch"
    )
    thread.start()
    thread.join()

    # --- Data Processing ---
    discovered_switches = {}      # Key: dpid_str, Value: {label, raw_id}
    active_links_from_chain = set() # Set of canonical link tuples: frozenset({(dpid1, port1), (dpid2, port2)})
    processing_warnings = []
    processing_errors = []

    blockchain_data = results.get("blockchain", {"data": [], "error": "Fetch thread did not complete"}) # Default if key missing
    if isinstance(blockchain_data, dict) and "error" in blockchain_data:
        processing_errors.append(f"Blockchain data fetch failed: {blockchain_data['error']}")
        chain_blocks = [] # Ensure chain_blocks is an empty list
    elif isinstance(blockchain_data, list):
        chain_blocks = blockchain_data
    else:
        processing_warnings.append(f"Unexpected format for blockchain data: {type(blockchain_data)}")
        chain_blocks = []


    # 1. Process Blocks and Transactions for Link Events
    if not chain_blocks and not processing_errors:
        processing_warnings.append("Blockchain is empty or no blocks were fetched.")

    for block_idx, block in enumerate(chain_blocks):
        if not isinstance(block, dict):
            processing_warnings.append(f"Block at index {block_idx} is not a dictionary. Skipping.")
            continue
        
        transactions = block.get("Transactions")
        if not transactions: # Handles null or empty list
            # logging.debug(f"Block #{block.get('sequence_nb')} has no transactions.")
            continue

        for tx_idx, tx in enumerate(transactions):
            if not isinstance(tx, dict) or "transaCore" not in tx or \
               not isinstance(tx["transaCore"], dict) or "Data" not in tx["transaCore"]:
                processing_warnings.append(f"Malformed transaction in Block #{block.get('sequence_nb')}, Tx Index {tx_idx}. Skipping.")
                continue

            tx_data = tx["transaCore"]["Data"]
            if not isinstance(tx_data, dict):
                # This can happen if Data is null or not an object (e.g. for BrutData if it was just a string)
                # processing_warnings.append(f"Transaction Data in Block #{block.get('sequence_nb')} is not a dictionary. Type: {type(tx_data)}. Skipping specific event check.")
                continue


            # Check if it's a LinkEventInput by looking for characteristic keys
            if all(k in tx_data for k in ["dpid1", "port1", "dpid2", "port2", "status"]):
                try:
                    dpid1 = str(tx_data["dpid1"])
                    port1 = int(tx_data["port1"])
                    dpid2 = str(tx_data["dpid2"])
                    port2 = int(tx_data["port2"])
                    status = str(tx_data["status"]).upper() # Normalize to uppercase

                    # Add switches to our discovered_switches list if not already there
                    for dpid_str in [dpid1, dpid2]:
                        if dpid_str not in discovered_switches:
                            try:
                                switch_num = int(dpid_str, 16)
                                label = f"s{switch_num}"
                            except (ValueError, TypeError):
                                label = f"s_{dpid_str[-4:]}"
                            discovered_switches[dpid_str] = {'label': label, 'raw_id': dpid_str}

                    # Canonical link representation
                    ep1 = (dpid1, port1)
                    ep2 = (dpid2, port2)
                    link_canonical = frozenset({ep1, ep2}) # Use frozenset for hashability in set

                    if status == "LINK_UP":
                        active_links_from_chain.add(link_canonical)
                    elif status == "LINK_DOWN":
                        active_links_from_chain.discard(link_canonical)
                    else:
                        processing_warnings.append(f"Unknown link status '{tx_data['status']}' in tx {tx.get('hash')}")

                except (KeyError, ValueError, TypeError) as e:
                    processing_warnings.append(f"Error processing LinkEventInput-like transaction data in Block #{block.get('sequence_nb')}: {e}. Data: {tx_data}")
                    logging.exception("Exception during LinkEventInput processing")

    # --- Prepare data for UI ---
    # discovered_nodes will now only contain switches
    discovered_nodes_for_graph = {}
    for dpid, sw_info in discovered_switches.items():
        discovered_nodes_for_graph[dpid] = {
            'type': 'switch',
            'label': sw_info['label'],
            'raw_id': dpid
        }

    # discovered_links_for_graph from active_links_from_chain
    discovered_links_for_graph = {} # Key: canonical tuple frozenset, Value: {src_label, dst_label, ...}
    for link_fs in active_links_from_chain:
        # Unpack frozenset (order might vary, but content is fixed)
        # Convert back to a list of tuples to sort and ensure consistent src/dst for display if needed
        # Though for agraph, the (source, target) doesn't strictly need this pre-sorting for the edge itself
        eps = list(link_fs)
        ep1_dpid, ep1_port = eps[0]
        ep2_dpid, ep2_port = eps[1]

        # It's useful to have labels for the graph edges
        src_label = discovered_switches.get(ep1_dpid, {}).get('label', ep1_dpid)
        dst_label = discovered_switches.get(ep2_dpid, {}).get('label', ep2_dpid)

        discovered_links_for_graph[link_fs] = {
            'src_dpid': ep1_dpid, 'src_port': ep1_port, 'src_label': src_label,
            'dst_dpid': ep2_dpid, 'dst_port': ep2_port, 'dst_label': dst_label,
        }

    # Host details and related port maps are no longer populated from blockchain
    host_details = OrderedDict()
    port_to_connected_node_map = {} # Will only contain switch-to-switch for now
    port_to_host_details = {}

    # Populate port_to_connected_node_map for switch-to-switch links
    for link_fs, link_info_val in discovered_links_for_graph.items():
        src_dpid_val, src_port_val = link_info_val['src_dpid'], link_info_val['src_port']
        dst_dpid_val, dst_port_val = link_info_val['dst_dpid'], link_info_val['dst_port']
        
        port_to_connected_node_map[f"{src_dpid_val}:{src_port_val}"] = link_info_val['dst_label']
        port_to_connected_node_map[f"{dst_dpid_val}:{dst_port_val}"] = link_info_val['src_label']


    end_time = time.time()
    processing_time = end_time - start_time
    logging.info(f"--- Blockchain Data Fetch & Processing finished in {processing_time:.2f} seconds ---")

    pbft_node_connection_info = f"{pbft_node_ip}:{pbft_node_http_port}"

    return (
        discovered_switches,        # For Switch Details UI {dpid: {info}}
        host_details,               # Empty OrderedDict
        discovered_nodes_for_graph, # For Graph Nodes (switches only) {dpid: {info}}
        discovered_links_for_graph, # For Graph Switch-Switch Edges {frozenset_link_key: {info}}
        port_to_connected_node_map, # For Switch Port Table (switch links only)
        port_to_host_details,       # Empty
        pbft_node_connection_info,
        processing_warnings,
        processing_errors,
        processing_time
    )


# --- Streamlit App UI ---
st.set_page_config(layout="wide", page_title="PBFT Blockchain Network Viewer")
st.title("PBFT Consensus-Agreed Network Topology Viewer")

# --- Connection Info (Sidebar) ---
st.sidebar.header("PBFT Node HTTP Endpoint")
pbft_ip = st.sidebar.text_input("Node IP:", value=DEFAULT_PBFT_NODE_IP)
pbft_port = st.sidebar.number_input("Node HTTP Port:", min_value=1, max_value=65535, value=DEFAULT_PBFT_NODE_HTTP_PORT)

if st.sidebar.button("🔄 Refresh Data"):
    st.cache_data.clear() # Clear cache for fetch_topology_from_blockchain
    st.rerun()

# --- Fetch and Process Data ---
fetch_start_time = time.time()
try:
    switch_details_from_chain, host_details_from_chain, graph_nodes_from_chain, \
        graph_links_from_chain, port_map_from_chain, port_host_map_from_chain, \
        connection_info, warnings, errors, processing_time = fetch_topology_from_blockchain(
            pbft_node_ip=pbft_ip,
            pbft_node_http_port=pbft_port,
            timeout=API_TIMEOUT
        )
    fetch_duration = time.time() - fetch_start_time
except Exception as e:
    st.error(f"An critical error occurred during data fetching or processing: {e}")
    logging.exception("Top-level error during data fetch/process from blockchain")
    st.stop()


# --- Display Status Messages (Sidebar) ---
st.sidebar.markdown("---")
st.sidebar.subheader("Fetch Status")
status_expanded = bool(errors or warnings)
with st.sidebar.expander("Show Fetch/Processing Logs", expanded=status_expanded):
    if errors:
        for error_msg_item in errors: st.error(error_msg_item) # Renamed variable
    if warnings:
        for warning_msg_item in warnings: st.warning(warning_msg_item) # Renamed variable
    if not errors and not warnings:
        st.success("Blockchain data fetched and processed successfully.")
    elif not errors and warnings:
        st.info("Blockchain data fetched with some processing warnings.")
    else:
         st.warning("Blockchain data fetch or processing encountered errors.")

st.sidebar.info(f"Queried: {connection_info}")
st.sidebar.caption(f"Timestamp: {time.strftime('%Y-%m-%d %H:%M:%S')}")
st.sidebar.caption(f"Fetch/Process Time: {processing_time:.2f}s (Total: {fetch_duration:.2f}s)")


# --- Handle No Data Case ---
if not graph_nodes_from_chain and not errors: # Check graph_nodes as it's built from switches
    st.warning("No switch topology data discovered from the blockchain's LinkEvents.")
    st.stop()
elif not graph_nodes_from_chain and errors:
     st.error("Failed to fetch topology data from blockchain. Check connection and logs.")
     st.stop()

# --- Build Graph Data ---
agraph_nodes = []
agraph_edges = []
edge_added_label_pairs = set()


# 1. Create Nodes from graph_nodes_from_chain (which are switches)
for node_id, node_info in graph_nodes_from_chain.items():
    node_label = node_info['label']
    raw_id = node_info['raw_id'] # DPID for switch
    tooltip = f"Label: {node_label}\nType: Switch\nID: {raw_id}"
    shape = "square"
    size = 15
    color = SWITCH_COLOR
    # Port count not directly available here unless we parse all LinkEvents again for each switch
    # For simplicity, we omit port count from tooltip in this version
    agraph_nodes.append(Node(id=node_label, label=node_label, title=tooltip, shape=shape, color=color, size=size))

# 2. Create Switch-to-Switch Edges from graph_links_from_chain
for link_key_fs, link_info_val in graph_links_from_chain.items():
    # link_info_val contains src_label, dst_label, src_port, dst_port etc.
    u_label = link_info_val['src_label']
    v_label = link_info_val['dst_label']
    src_port_display = link_info_val['src_port'] # Just port number for now
    dst_port_display = link_info_val['dst_port']

    u_node_exists = any(n.id == u_label for n in agraph_nodes)
    v_node_exists = any(n.id == v_label for n in agraph_nodes)

    if u_node_exists and v_node_exists:
        edge_label_pair = tuple(sorted((u_label, v_label))) # Use labels for uniqueness check
        # Check if this specific pair of (u_label, v_label) with these port details has been added.
        # Since graph_links_from_chain key is the frozenset of (dpid,port) tuples,
        # it should already be unique per logical link.
        # The edge_added_label_pairs is more about preventing visual duplicates if processing logic was different.
        # Here, since graph_links_from_chain is built from a set, it's inherently unique.
        
        # For this version, we can simplify the duplicate check as graph_links_from_chain is already unique links
        # if edge_label_pair not in edge_added_label_pairs: # This check is fine.
        tooltip = f"{u_label} (Port {src_port_display}) <-> {v_label} (Port {dst_port_display})"
        edge_color = {"color": LINK_COLOR, "highlight": LINK_HIGHLIGHT_COLOR}
        agraph_edges.append(Edge(source=u_label, target=v_label, color=edge_color, title=tooltip, dashes=False))
            # edge_added_label_pairs.add(edge_label_pair) # Not strictly needed if graph_links_from_chain is from a set
    else:
         logging.warning(f"Skipping graph edge: Node(s) not found for labels {u_label} or {v_label}")


# --- Display Graph and Details ---
col1, col2 = st.columns([3, 2], gap="large")

with col1:
    st.subheader("Consensus-Agreed Network Topology Graph (Switches & Links)")
    legend_html = f"""
    <div style="margin-bottom: 10px;">
        <b>Legend:</b>
        <span style="margin-left: 15px; vertical-align: middle;">
            <span style="height:12px; width:12px; background-color:{SWITCH_COLOR}; border-radius:0%; display: inline-block; vertical-align: middle; margin-right: 5px;"></span>Switch
        </span>
        <span style="margin-left: 15px; vertical-align: middle;">
            <span style="display: inline-block; width: 25px; border-bottom: 2px solid {LINK_COLOR}; vertical-align: middle; margin-right: 5px;"></span> Link
        </span>
    </div>
    """
    st.markdown(legend_html, unsafe_allow_html=True)

    config = Config(width='100%', height=650,
                    directed=False, physics=True, hierarchical=False,
                    physics_settings={
                        "solver": "barnesHut",
                        "barnesHut": {"gravitationalConstant": -18000, "centralGravity": 0.1,
                                      "springLength": 120, "springConstant": 0.05,
                                      "damping": 0.15, "avoidOverlap": 0.3},
                        "minVelocity": 0.75,
                        "stabilization": {"iterations": 250}
                    },
                    interaction={"tooltipDelay": 150, "hideEdgesOnDrag": False, "hover": True},
                    nodes={"font": {"size": 12, "face": "tahoma"}},
                    edges={"width": 1.5, "smooth": {"enabled": True, "type": "continuous"},
                           "arrows": {"to": {"enabled": False}}})

    if agraph_nodes:
        try:
            agraph(nodes=agraph_nodes, edges=agraph_edges, config=config)
        except Exception as e:
            st.error(f"Error rendering graph: {e}")
            logging.exception("Graph rendering error")
    else:
        st.warning("No switch nodes derived from blockchain LinkEvents to display in the graph.")


with col2:
    tab1, tab2 = st.tabs(["Switch Details", "Host Details (N/A from Blockchain)"])

    with tab1:
        st.subheader("Switch Details (from Blockchain LinkEvents)")
        if not switch_details_from_chain: # This is the dict {dpid: {label, raw_id}}
            st.warning("No switch data derived from blockchain LinkEvents.")
        else:
            def sort_key_switch_bc(dpid_bc):
                label_bc = switch_details_from_chain[dpid_bc]['label']
                try: return int(label_bc[1:])
                except ValueError: return label_bc
            sorted_switch_dpids_bc = sorted(switch_details_from_chain.keys(), key=sort_key_switch_bc)
            display_labels_bc = [switch_details_from_chain[dpid_bc]['label'] for dpid_bc in sorted_switch_dpids_bc]

            if not display_labels_bc:
                 st.info("No switches found from blockchain LinkEvents.")
            else:
                label_to_dpid_bc = {switch_details_from_chain[dpid_bc]['label']: dpid_bc for dpid_bc in sorted_switch_dpids_bc}
                selected_switch_label_bc = st.selectbox("Select Switch:", display_labels_bc, key="switch_select_bc")

                if selected_switch_label_bc and selected_switch_label_bc in label_to_dpid_bc:
                    selected_dpid_bc = label_to_dpid_bc[selected_switch_label_bc]
                    selected_switch_info_bc = switch_details_from_chain[selected_dpid_bc]

                    st.markdown(f"**Details for:** `{selected_switch_info_bc['label']}` (`{selected_dpid_bc}`)")
                    st.text(f"DPID: {selected_dpid_bc}")

                    st.markdown("**Connected Ports (from LinkEvents)**")
                    # Reconstruct port connections for this switch from active_links_from_chain
                    # (which is now graph_links_from_chain in the main scope)
                    connected_ports_info = []
                    for link_fs_key, link_val in graph_links_from_chain.items():
                        if link_val['src_dpid'] == selected_dpid_bc:
                            connected_ports_info.append({
                                "Local Port": link_val['src_port'],
                                "Connected To Switch": link_val['dst_label'],
                                "Remote Port": link_val['dst_port']
                            })
                        elif link_val['dst_dpid'] == selected_dpid_bc:
                             connected_ports_info.append({
                                "Local Port": link_val['dst_port'],
                                "Connected To Switch": link_val['src_label'],
                                "Remote Port": link_val['src_port']
                            })
                    
                    if connected_ports_info:
                        # Sort by local port number
                        connected_ports_info.sort(key=lambda x: x['Local Port'])
                        port_df_bc = pd.DataFrame(connected_ports_info)
                        st.dataframe(port_df_bc, hide_index=True, use_container_width=True)
                    else:
                        st.info("No active link events found involving this switch in the current blockchain view.")
                else:
                    st.error("Selected switch data not found (internal error).")

    with tab2:
        st.subheader("Host Details")
        st.info("Host information is not directly derived from LinkEvent transactions on the blockchain in this version of the viewer. To see hosts, the PBFT network would need to achieve consensus on host discovery events, or this UI would need to query a separate source (like Ryu's REST API) for host data.")
        if host_details_from_chain: # Should be empty
             st.write(host_details_from_chain) # Display if somehow populated