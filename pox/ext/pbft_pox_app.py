# /path/to/pox/ext/pbft_pox_app.py

import os
import json
import time
from concurrent import futures
import threading
import sys # Not strictly needed by this version, but often useful
import logging # Standard Python logging, POX uses its own wrapper mostly

# Third-party imports
import grpc

# --- Your generated gRPC files (ensure they are in pox/ext/ or your PYTHONPATH) ---
import pbft_consensus_pb2
import pbft_consensus_pb2_grpc
from google.protobuf import empty_pb2 # For google.protobuf.Empty

# Import POX core and OpenFlow stuff
from pox.core import core
import pox.openflow.libopenflow_01 as of
from pox.lib.packet import ethernet
from pox.lib.packet import arp 
from pox.lib.util import dpid_to_str
from pox.openflow.discovery import LinkEvent, Discovery

# Get the main logger for this component
log = core.getLogger("PBFT_POX") # Get logger BEFORE launch

# --- Configuration (Defaults, overridden by launch args) ---
DEFAULT_PBFT_DEALER_ENDPOINT = '127.0.0.1:50051'
DEFAULT_REPLICA_ENDPOINT_BASE = '0.0.0.0'
DEFAULT_REPLICA_START_PORT = 50052

# --- Global variables ---
_is_primary = True
_pbft_dealer_endpoint = DEFAULT_PBFT_DEALER_ENDPOINT
_replica_port = None # For replicas, their listening port

_grpc_stub_dealer = None
_grpc_channel_dealer = None
_grpc_server_replica = None
_grpc_server_thread = None # Thread running the replica gRPC server
_l2_logic = None # Instance of DeterministicL2SwitchLogic


# --- Deterministic Logic ---
class DeterministicL2SwitchLogic:
    def __init__(self, logger):
        self.mac_to_port = {} # Key: dpid_str, Value: {mac_addr_str: out_port_int}
        self.topology_active_links = set() # Set of frozensets of ((dpid1, port1), (dpid2, port2))
        self.logger = logger
        self.logger.info("DeterministicL2SwitchLogic initialized.")

    def learn_mac(self, dpid_str, src_mac_str, in_port):
        if dpid_str not in self.mac_to_port:
            self.mac_to_port[dpid_str] = {}
            self.logger.debug(f"DPID {dpid_str}: Initialized MAC table.")
        if src_mac_str not in self.mac_to_port[dpid_str] or self.mac_to_port[dpid_str][src_mac_str] != in_port:
            self.mac_to_port[dpid_str][src_mac_str] = in_port
            self.logger.debug(f"DPID {dpid_str}: Learned/Updated MAC {src_mac_str} on port {in_port}.")

    def compute_action(self, dpid_str, in_port, pkt_data):
        self.logger.debug(f"DPID {dpid_str}: Computing action for PacketIn on port {in_port}.")
        try:
            pkt = ethernet(pkt_data)
        except Exception as e:
            self.logger.error(f"DPID {dpid_str}: Error parsing packet: {e}")
            return None

        if not pkt.parsed:
            self.logger.debug(f"DPID {dpid_str}: Could not parse Ethernet packet.")
            return None
        # Ignore LLDP (used by discovery) and IPv6 for this simple L2 switch
        if pkt.type == ethernet.LLDP_TYPE or pkt.type == ethernet.IPV6_TYPE: # Use defined constants
             self.logger.debug(f"DPID {dpid_str}: Skipping LLDP/IPv6 packet (type: {pkt.type:#06x}).")
             return None

        dst_mac_str = str(pkt.dst)
        src_mac_str = str(pkt.src)

        self.learn_mac(dpid_str, src_mac_str, in_port)

        action_dict = None
        # Check if DPID is known (it should be after learning if it's the source's DPID)
        if dpid_str in self.mac_to_port:
            if dst_mac_str in self.mac_to_port[dpid_str]:
                out_port = self.mac_to_port[dpid_str][dst_mac_str]
                if out_port != in_port:
                    action_dict = {'type': 'packet_out', 'out_port': out_port}
                    self.logger.debug(f"DPID {dpid_str}: Known DST {dst_mac_str}, action packet_out to port {out_port}.")
                else: # Destination is on the same port packet came in from
                    self.logger.debug(f"DPID {dpid_str}: DST {dst_mac_str} known on input port {in_port}. No packet_out action needed (drop/ignore).")
                    action_dict = None # No action implies drop by controller or let switch handle if no flow
            else: # Destination MAC unknown on this switch
                action_dict = {'type': 'packet_out', 'out_port': of.OFPP_FLOOD}
                self.logger.debug(f"DPID {dpid_str}: Unknown DST {dst_mac_str}, action packet_out FLOOD.")
        else: # DPID itself is unknown (shouldn't happen if learning occurs for src_mac)
            action_dict = {'type': 'packet_out', 'out_port': of.OFPP_FLOOD}
            self.logger.warning(f"DPID {dpid_str}: DPID not found in MAC table! Defaulting to FLOOD.")
        return action_dict

    def update_link_state(self, dpid1_str, port1, dpid2_str, port2, status_enum):
        # Canonical representation of a link
        # Sort by DPID, then port to ensure ((d1,p1),(d2,p2)) is same as ((d2,p2),(d1,p1))
        ep1 = (dpid1_str, int(port1))
        ep2 = (dpid2_str, int(port2))
        # Ensure canonical order for the link tuple
        link_endpoints = tuple(sorted((ep1, ep2)))

        status_name = pbft_consensus_pb2.LinkEventInfo.LinkStatus.Name(status_enum)
        self.logger.info(f"DeterministicLogic: Updating link state: {dpid1_str}.{port1}-{dpid2_str}.{port2} ({link_endpoints}) -> {status_name}")

        if status_enum == pbft_consensus_pb2.LinkEventInfo.LINK_UP:
            self.topology_active_links.add(link_endpoints)
            self.logger.debug(f"Link UP: {link_endpoints} added to topology. Active links: {len(self.topology_active_links)}")
        elif status_enum == pbft_consensus_pb2.LinkEventInfo.LINK_DOWN:
            self.topology_active_links.discard(link_endpoints) # discard doesn't raise error if not found
            self.logger.debug(f"Link DOWN: {link_endpoints} removed from topology. Active links: {len(self.topology_active_links)}")
            # Potentially clear MAC entries related to this link going down.
            # This can be complex. A simpler approach is to let MAC entries time out or be re-learned.
            # For example, if a port involved in the link goes down:
            self._clear_macs_for_port(dpid1_str, port1)
            self._clear_macs_for_port(dpid2_str, port2)
        else:
            self.logger.warning(f"DeterministicLogic: Received unknown link status enum {status_enum} for link {link_endpoints}")

        # Log the current topology (can be verbose)
        # self.logger.debug(f"Current active links: {self.topology_active_links}")

    def _clear_macs_for_port(self, dpid_str, port_no):
        if dpid_str in self.mac_to_port:
            macs_to_remove = [mac for mac, learned_port in self.mac_to_port[dpid_str].items() if learned_port == port_no]
            if macs_to_remove:
                self.logger.info(f"DPID {dpid_str}: Clearing {len(macs_to_remove)} MAC entries for port {port_no} due to link change.")
                for mac in macs_to_remove:
                    del self.mac_to_port[dpid_str][mac]
                if not self.mac_to_port[dpid_str]: # If DPID's MAC table is empty
                    del self.mac_to_port[dpid_str]
                    self.logger.debug(f"DPID {dpid_str}: MAC table now empty.")


# --- Replica gRPC Server Implementation ---
class PoxReplicaServicer(pbft_consensus_pb2_grpc.RyuReplicaLogicServicer):
    def CalculateAction(self, request, context):
        handler_log = core.getLogger("PBFT_POX_ReplicaHandler")
        peer = context.peer()
        handler_log.info(f"Replica: Received CalculateAction from {peer}")
        handler_log.debug(f"  PacketInfo: DPID={request.packet_info.dpid}, InPort={request.packet_info.in_port}, BufID={request.packet_info.buffer_id}")

        computed_action_json = "{}"
        if _l2_logic:
            try:
                 action_dict = _l2_logic.compute_action(
                     request.packet_info.dpid,
                     request.packet_info.in_port,
                     request.packet_info.data
                 )
                 if action_dict:
                     computed_action_json = json.dumps(action_dict)
                 handler_log.debug(f"Replica: Deterministic logic computed: {action_dict}")
            except Exception as e:
                handler_log.exception(f"Replica: Error in deterministic logic for {peer}: {e}")
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(f"Error computing action: {e}")
                # Fallback to empty action
                return pbft_consensus_pb2.CalculateActionResponse(computed_action=pbft_consensus_pb2.Action(action_json="{}"))
        else:
            handler_log.error("Replica: L2 Logic not initialized!")
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            context.set_details("Replica logic unavailable")
            return pbft_consensus_pb2.CalculateActionResponse(computed_action=pbft_consensus_pb2.Action(action_json="{}"))

        response_action = pbft_consensus_pb2.Action(action_json=computed_action_json)
        handler_log.info(f"Replica: Sending computed action to {peer}: {computed_action_json}")
        return pbft_consensus_pb2.CalculateActionResponse(computed_action=response_action)

    def NotifyLinkEvent(self, request, context):
        handler_log = core.getLogger("PBFT_POX_ReplicaHandler")
        peer = context.peer()
        status_name = pbft_consensus_pb2.LinkEventInfo.LinkStatus.Name(request.status)
        handler_log.info(f"Replica: Received NotifyLinkEvent from {peer}: "
                         f"{request.dpid1}.{request.port1} <-> {request.dpid2}.{request.port2} is {status_name}, ts={request.timestamp_ns}")

        if not _l2_logic:
            handler_log.error("Replica: L2 Logic not initialized! Cannot update link state.")
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            context.set_details("Replica logic unavailable")
            return empty_pb2.Empty() # Use imported Empty
        try:
            _l2_logic.update_link_state(
                request.dpid1, request.port1,
                request.dpid2, request.port2,
                request.status # Pass the enum value directly
            )
            handler_log.debug(f"Replica: Updated link state successfully from {peer}.")
        except Exception as e:
            handler_log.exception(f"Replica: Error updating link state from {peer}: {e}")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Error updating link state: {e}")
        return empty_pb2.Empty() # Use imported Empty


def _run_grpc_server(port_to_listen): # Renamed arg for clarity
    global _grpc_server_replica # Allow modification of the global variable
    thread_log = core.getLogger("PBFT_POX_gRPCThread") # More specific logger name
    thread_log.info(f"Replica gRPC Server Thread: Starting on port {port_to_listen}")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10)) # Standard worker count
    pbft_consensus_pb2_grpc.add_RyuReplicaLogicServicer_to_server(
        PoxReplicaServicer(), server
    )
    listen_addr = f'[::]:{port_to_listen}'
    server.add_insecure_port(listen_addr)
    _grpc_server_replica = server # Store the server instance globally
    try:
        server.start()
        thread_log.info(f"Replica gRPC Server Thread: Listening on {listen_addr}")
        server.wait_for_termination() # Blocks until server.stop() is called
    except Exception as e:
        thread_log.exception(f"Replica gRPC Server Thread: Runtime error - {e}")
    finally:
        _grpc_server_replica = None # Clear global ref on exit
        thread_log.info("Replica gRPC Server Thread: Stopped.")


# --- Main POX Component Logic ---
class PoxPbftApp:
    def __init__(self):
        log.info("PoxPbftApp initializing...")
        if _is_primary:
            self.connect_to_dealer()
            def register_discovery_listener():
                if core.hasComponent("openflow_discovery"): 
                    discovery_component = core.components["openflow_discovery"]
                    discovery_component.addListenerByName(LinkEvent.__name__, self._handle_LinkEvent)
                    log.info("Primary: Registered LinkEvent listener with openflow_discovery component.")
                else:
                    log.warning("Primary: openflow_discovery component not found. LinkEvent listener NOT registered.")
            core.call_when_ready(register_discovery_listener, ["openflow_discovery"])
        core.openflow.addListeners(self) # For ConnectionUp, PacketIn (if primary)
        log.info("PoxPbftApp OpenFlow listeners registered.")

    def connect_to_dealer(self):
        global _grpc_channel_dealer, _grpc_stub_dealer
        if not _pbft_dealer_endpoint:
            log.error("Primary: PBFT_DEALER_ENDPOINT not configured.")
            return
        try:
            log.info(f"Primary: Connecting to PBFT Dealer at {_pbft_dealer_endpoint}")
            _grpc_channel_dealer = grpc.insecure_channel(_pbft_dealer_endpoint)
            _grpc_stub_dealer = pbft_consensus_pb2_grpc.PBFTConsensusStub(_grpc_channel_dealer)
            log.info("Primary: gRPC stub for PBFT Dealer created.")
        except Exception as e:
            log.exception(f"Primary: Failed to create gRPC channel/stub to Dealer: {e}")
            if _grpc_channel_dealer:
                try: _grpc_channel_dealer.close()
                except: pass
            _grpc_channel_dealer = None
            _grpc_stub_dealer = None

    def _handle_ConnectionUp(self, event):
        dpid_str = dpid_to_str(event.dpid)
        log.info(f"Switch {dpid_str} connected.")
        msg = of.ofp_flow_mod(command=of.OFPFC_ADD) # Explicitly ADD
        msg.match = of.ofp_match() # Match all packets
        msg.actions.append(of.ofp_action_output(port = of.OFPP_CONTROLLER))
        msg.priority = 0 # Lowest priority, so other flows can override
        event.connection.send(msg)
        log.info(f"Installed table-miss flow entry for switch {dpid_str} to send to controller.")

    def _handle_LinkEvent(self, event):
        # This handler is only active if _is_primary (due to listener registration in __init__)
        dpid1_str = dpid_to_str(event.link.dpid1)
        port1 = event.link.port1
        dpid2_str = dpid_to_str(event.link.dpid2)
        port2 = event.link.port2

        status_proto = pbft_consensus_pb2.LinkEventInfo.LINK_STATUS_UNSPECIFIED
        status_log_str = "UNKNOWN"

        if event.added:
            status_proto = pbft_consensus_pb2.LinkEventInfo.LINK_UP
            status_log_str = "UP"
            log.info(f"Primary: Link {status_log_str}: {dpid1_str}.{port1} <-> {dpid2_str}.{port2}")
        elif event.removed:
            status_proto = pbft_consensus_pb2.LinkEventInfo.LINK_DOWN
            status_log_str = "DOWN"
            log.info(f"Primary: Link {status_log_str}: {dpid1_str}.{port1} <-> {dpid2_str}.{port2}")
        else: # Should not occur for LinkEvent
            return

        if not _grpc_stub_dealer:
            log.error(f"Primary: No Dealer connection to report link event {status_log_str} for {dpid1_str} - {dpid2_str}.")
            return

        link_event_proto = pbft_consensus_pb2.LinkEventInfo(
            dpid1=dpid1_str,
            port1=port1,
            dpid2=dpid2_str,
            port2=port2,
            status=status_proto,
            timestamp_ns=int(time.time() * 1e9) # Current time in nanoseconds
        )

        try:
            log.debug(f"Primary: Reporting LinkEvent ({status_log_str}) to Dealer: {link_event_proto}")
            # The ReportLinkEvent RPC returns Empty, so no response variable needed unless for error checking
            _grpc_stub_dealer.ReportLinkEvent(link_event_proto, timeout=10) # Add a timeout
            log.info(f"Primary: Successfully reported LinkEvent ({status_log_str}) to Dealer for {dpid1_str}-{dpid2_str}.")
        except grpc.RpcError as e_rpc:
            log.error(f"Primary: gRPC ReportLinkEvent failed for {dpid1_str}-{dpid2_str}: {e_rpc.code()} - {e_rpc.details()}")
        except Exception as e_gen:
            log.exception(f"Primary: Unexpected error reporting LinkEvent for {dpid1_str}-{dpid2_str}: {e_gen}")


    def _handle_PacketIn(self, event):
        if not _is_primary:
            return

        packet = event.parsed
        if not packet.parsed:
            log.warning("Primary: Ignoring incompletely parsed packet.")
            return
        # Use constants for LLDP and IPV6 types for clarity
        if packet.type == ethernet.LLDP_TYPE or packet.type == ethernet.IPV6_TYPE:
            return

        if not _l2_logic:
            log.error(f"Primary (DPID {dpid_to_str(event.dpid)}): L2 Logic not initialized! Cannot process PacketIn.")
            return

        dpid_str = dpid_to_str(event.dpid)
        in_port = event.port
        proposed_action_dict = _l2_logic.compute_action(dpid_str, in_port, event.data)

        if not proposed_action_dict: # If logic returns None (e.g., drop or no action)
            log.info(f"Primary (DPID {dpid_str}): Deterministic logic proposed no action. PacketIn not sent for consensus.")
            return

        proposed_action_json = json.dumps(proposed_action_dict)
        log.info(f"Primary (DPID {dpid_str}): Proposed action for PacketIn: {proposed_action_json}")

        if not _grpc_stub_dealer:
            log.error(f"Primary (DPID {dpid_str}): No Dealer connection. Cannot request consensus for PacketIn.")
            return

        ofp_msg = event.ofp # Original OpenFlow message from the event
        packet_info_proto = pbft_consensus_pb2.PacketInfo(
            dpid=dpid_str,
            in_port=in_port,
            buffer_id=ofp_msg.buffer_id if ofp_msg.buffer_id is not None and ofp_msg.buffer_id != 0xffffffff else 0,
            total_len=ofp_msg.total_len if ofp_msg.total_len is not None else len(event.data),
            data=event.data
        )
        proposed_action_proto = pbft_consensus_pb2.Action(action_json=proposed_action_json)
        request_proto = pbft_consensus_pb2.ConsensusRequest(
            packet_info=packet_info_proto,
            proposed_action=proposed_action_proto
        )

        # Use a daemon thread for the gRPC call to avoid blocking POX core
        thread = threading.Thread(target=self._request_consensus_in_thread, args=(event, request_proto), daemon=True)
        thread.start()

    def _request_consensus_in_thread(self, event, request_proto):
        dpid_str = dpid_to_str(event.dpid)
        if not _grpc_stub_dealer: # Re-check in thread context
            log.error(f"Primary Thread (DPID {dpid_str}): Dealer stub unavailable. Cannot send ConsensusRequest.")
            return
        try:
            log.info(f"Primary Thread (DPID {dpid_str}): Sending ConsensusRequest to Dealer...")
            start_time = time.time()
            response = _grpc_stub_dealer.RequestConsensus(request_proto, timeout=30) # 30s timeout
            latency = time.time() - start_time
            log.info(f"Primary Thread (DPID {dpid_str}): Received ConsensusResponse from Dealer in {latency:.4f}s. "
                     f"Consensus={response.consensus_reached}, Status='{response.status_message}'")
            core.callLater(self._execute_final_action, event, response)
        except grpc.RpcError as e_rpc:
            latency = time.time() - start_time
            log.error(f"Primary Thread (DPID {dpid_str}): gRPC ConsensusRequest failed after {latency:.4f}s: {e_rpc.code()} - {e_rpc.details()}")
        except Exception as e_gen:
            latency = time.time() - start_time
            log.exception(f"Primary Thread (DPID {dpid_str}): Unexpected error during ConsensusRequest after {latency:.4f}s: {e_gen}")

    def _execute_final_action(self, event, response_proto):
        dpid_str = dpid_to_str(event.dpid)
        if not response_proto:
             log.warning(f"Primary Execute (DPID {dpid_str}): Received None response_proto from Dealer.")
             return
        if not response_proto.consensus_reached:
            log.warning(f"Primary Execute (DPID {dpid_str}): Consensus not reached. Status: {getattr(response_proto, 'status_message', 'N/A')}")
            return
        if not response_proto.final_action or not response_proto.final_action.action_json:
            log.warning(f"Primary Execute (DPID {dpid_str}): Consensus reached, but no final_action provided.")
            return

        final_action_json = response_proto.final_action.action_json
        if final_action_json == "{}" or final_action_json is None:
            log.info(f"Primary Execute (DPID {dpid_str}): Received empty final_action (no-op).")
            return

        try:
            action_dict = json.loads(final_action_json)
            log.info(f"Primary Execute (DPID {dpid_str}): Executing final action: {action_dict}")
            action_type = action_dict.get('type')

            if action_type == 'packet_out':
                out_port_val = action_dict.get('out_port')
                if out_port_val is None:
                    log.error(f"Execute (DPID {dpid_str}): packet_out missing 'out_port'.")
                    return

                if isinstance(out_port_val, str) and out_port_val.upper() == 'FLOOD':
                    out_port = of.OFPP_FLOOD
                else:
                    try: out_port = int(out_port_val)
                    except (ValueError, TypeError):
                        log.error(f"Execute (DPID {dpid_str}): Invalid out_port '{out_port_val}'.")
                        return

                msg = of.ofp_packet_out()
                msg.actions.append(of.ofp_action_output(port=out_port))
                if event.ofp.buffer_id is not None and event.ofp.buffer_id != 0xffffffff:
                    msg.buffer_id = event.ofp.buffer_id
                else: # No valid buffer_id, send raw packet data
                    msg.buffer_id = 0xffffffff
                    msg.data = event.data
                msg.in_port = event.port
                event.connection.send(msg)
                log.debug(f"Execute (DPID {dpid_str}): Sent PacketOut to port {out_port_val}.")

            elif action_type == 'flow_mod':
                log.warning(f"Execute (DPID {dpid_str}): FlowMod execution not fully implemented.")
                # Placeholder for flow_mod logic
            else:
                log.warning(f"Execute (DPID {dpid_str}): Unknown action type '{action_type}'.")

        except json.JSONDecodeError:
            log.error(f"Primary Execute (DPID {dpid_str}): Failed to decode final_action_json: {final_action_json}")
        except Exception as e_exec:
            log.exception(f"Primary Execute (DPID {dpid_str}): Error executing final_action: {e_exec}")


def _cleanup():
    log.info("--- POX PBFT App Cleaning Up ---")
    global _grpc_channel_dealer, _grpc_server_replica, _grpc_server_thread
    if _grpc_channel_dealer:
        log.info("Primary: Closing gRPC channel to Dealer.")
        try: _grpc_channel_dealer.close()
        except Exception as e: log.error(f"Error closing dealer channel: {e}")
        _grpc_channel_dealer = None

    if _grpc_server_replica:
        log.info("Replica: Stopping gRPC server...")
        try: _grpc_server_replica.stop(grace=5.0) # 5s grace period
        except Exception as e: log.error(f"Error stopping replica gRPC server: {e}")
        _grpc_server_replica = None

    if _grpc_server_thread and _grpc_server_thread.is_alive():
        log.info("Replica: Waiting for gRPC server thread to join...")
        _grpc_server_thread.join(timeout=5.0)
        if _grpc_server_thread.is_alive():
            log.warning("Replica: gRPC server thread did not join gracefully.")
        _grpc_server_thread = None
    log.info("--- POX PBFT App Cleanup Complete ---")


# --- launch() function ---
def launch(primary="true", dealer_ip=None, dealer_port=None, replica_id=None):
    global _is_primary, _replica_port, _l2_logic, _pbft_dealer_endpoint, _grpc_server_thread

    # Make sure gRPC stubs are imported correctly
    if not hasattr(pbft_consensus_pb2, 'PacketInfo') or \
       not hasattr(pbft_consensus_pb2_grpc, 'PBFTConsensusStub'):
        log.critical("Failed to import generated gRPC stubs. Check paths and regeneration.")
        return

    _is_primary = primary.lower() == "true"
    _l2_logic = DeterministicL2SwitchLogic(core.getLogger("PBFT_POX_L2Logic")) # Use a specific logger for L2 logic

    if _is_primary:
        log.info("--- Starting PBFT POX App (Mode: PRIMARY) ---")
        if not dealer_ip or not dealer_port:
            log.error("Primary mode requires --dealer_ip and --dealer_port.")
            return
        _pbft_dealer_endpoint = f"{dealer_ip}:{dealer_port}"
        log.info(f"Primary: PBFT Dealer endpoint set to: {_pbft_dealer_endpoint}")
        core.registerNew(PoxPbftApp)
    else: # Replica mode
        log.info("--- Starting PBFT POX App (Mode: REPLICA) ---")
        if replica_id is None:
            log.error("Replica mode requires --replica_id.")
            return
        try:
            replica_id_int = int(replica_id)
            _replica_port = DEFAULT_REPLICA_START_PORT + replica_id_int # Calculate port
            log.info(f"Replica ID: {replica_id_int}, gRPC Port: {_replica_port}")

            # Start gRPC server in a daemon thread for this replica
            _grpc_server_thread = threading.Thread(
                target=_run_grpc_server,
                args=(_replica_port,), # Pass calculated port
                daemon=True,
                name=f"gRPCReplicaServer-{replica_id_int}"
            )
            _grpc_server_thread.start()
            log.info(f"Replica: gRPC server thread started for port {_replica_port}.")
        except ValueError:
            log.error(f"Invalid replica_id: '{replica_id}'. Must be an integer.")
            return
        except Exception as e:
            log.exception(f"Failed to start replica gRPC server thread: {e}")
            return

    # Register cleanup function for when POX is shutting down
    core.addListenerByName("GoingDownEvent", lambda event: _cleanup())
    log.info("PBFT POX Application Component Launched.")