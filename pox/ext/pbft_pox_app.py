# /path/to/pox/ext/pbft_pox_app.py

import os
import json
import time
from concurrent import futures
import threading
import sys
import logging
import hashlib # For creating a simple packet identifier

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
# from pox.lib.packet import arp # Keep for potential future use
from pox.lib.util import dpid_to_str
from pox.openflow.discovery import LinkEvent, Discovery # Import Discovery

# Get the main logger for this component
log = core.getLogger("PBFT_POX")

# --- Configuration ---
DEFAULT_PBFT_DEALER_ENDPOINT = '127.0.0.1:50051'
DEFAULT_REPLICA_ENDPOINT_BASE = '0.0.0.0'
DEFAULT_REPLICA_START_PORT = 50052
GRPC_TIMEOUT_PRIMARY_TO_DEALER = 30 # Seconds for RequestConsensus
GRPC_TIMEOUT_LINK_REPORT = 10       # Seconds for ReportLinkEvent

# --- Global variables ---
_is_primary = True
_pbft_dealer_endpoint = DEFAULT_PBFT_DEALER_ENDPOINT
_replica_port = None

_grpc_stub_dealer = None
_grpc_channel_dealer = None
_grpc_server_replica = None
_grpc_server_thread = None
_l2_logic = None
_packet_event_counter = 0 # Simple counter for PacketIn events for logging correlation
_packet_event_counter_lock = threading.Lock()

# --- Deterministic Logic ---
class DeterministicL2SwitchLogic:
    def __init__(self, logger):
        self.mac_to_port = {}
        self.topology_active_links = set()
        self.logger = logger
        self.logger.info("DeterministicL2SwitchLogic initialized.")

    def learn_mac(self, dpid_str, src_mac_str, in_port):
        if dpid_str not in self.mac_to_port:
            self.mac_to_port[dpid_str] = {}
            self.logger.debug(f"DPID {dpid_str}: Initialized MAC table.")
        if src_mac_str not in self.mac_to_port[dpid_str] or self.mac_to_port[dpid_str][src_mac_str] != in_port:
            self.mac_to_port[dpid_str][src_mac_str] = in_port
            self.logger.debug(f"DPID {dpid_str}: Learned/Updated MAC {src_mac_str} on port {in_port}.")

    def compute_action(self, dpid_str, in_port, pkt_data, packet_id_log_prefix=""): # Add packet_id_log_prefix
        self.logger.debug(f"{packet_id_log_prefix}DPID {dpid_str}: Computing action for PacketIn on port {in_port}.")
        try:
            pkt = ethernet(pkt_data)
        except Exception as e:
            self.logger.error(f"{packet_id_log_prefix}DPID {dpid_str}: Error parsing packet: {e}")
            return None

        if not pkt.parsed:
            self.logger.debug(f"{packet_id_log_prefix}DPID {dpid_str}: Could not parse Ethernet packet.")
            return None
        if pkt.type == ethernet.LLDP_TYPE or pkt.type == ethernet.IPV6_TYPE:
             self.logger.debug(f"{packet_id_log_prefix}DPID {dpid_str}: Skipping LLDP/IPv6 packet (type: {pkt.type:#06x}).")
             return None

        dst_mac_str = str(pkt.dst)
        src_mac_str = str(pkt.src)
        self.learn_mac(dpid_str, src_mac_str, in_port) # learning is part of the deterministic state change

        action_dict = None
        if dpid_str in self.mac_to_port:
            if dst_mac_str in self.mac_to_port[dpid_str]:
                out_port = self.mac_to_port[dpid_str][dst_mac_str]
                if out_port != in_port:
                    action_dict = {'type': 'packet_out', 'out_port': out_port}
                    self.logger.debug(f"{packet_id_log_prefix}DPID {dpid_str}: Known DST {dst_mac_str}, action packet_out to port {out_port}.")
                else:
                    self.logger.debug(f"{packet_id_log_prefix}DPID {dpid_str}: DST {dst_mac_str} known on input port {in_port}. No action.")
                    action_dict = None
            else:
                action_dict = {'type': 'packet_out', 'out_port': of.OFPP_FLOOD}
                self.logger.debug(f"{packet_id_log_prefix}DPID {dpid_str}: Unknown DST {dst_mac_str}, action packet_out FLOOD.")
        else:
            action_dict = {'type': 'packet_out', 'out_port': of.OFPP_FLOOD}
            self.logger.warning(f"{packet_id_log_prefix}DPID {dpid_str}: DPID not in MAC table! Defaulting to FLOOD.")
        self.logger.debug(f"{packet_id_log_prefix}Deterministic logic returning action: {action_dict}")
        return action_dict

    def update_link_state(self, dpid1_str, port1, dpid2_str, port2, status_enum):
        current_time_ns = time.time_ns() # Get current time for logging
        ep1 = (dpid1_str, int(port1))
        ep2 = (dpid2_str, int(port2))
        link_endpoints = tuple(sorted((ep1, ep2)))
        status_name = pbft_consensus_pb2.LinkEventInfo.LinkStatus.Name(status_enum)

        log_prefix = f"[TOPOLOGY_REPLICA][{current_time_ns}] "
        self.logger.info(f"{log_prefix}DeterministicLogic: Updating link state: {dpid1_str}.{port1}-{dpid2_str}.{port2} ({link_endpoints}) -> {status_name}")

        if status_enum == pbft_consensus_pb2.LinkEventInfo.LINK_UP:
            if link_endpoints not in self.topology_active_links:
                self.topology_active_links.add(link_endpoints)
                self.logger.info(f"{log_prefix}Link UP: {link_endpoints} added. Active links: {len(self.topology_active_links)}")
            else:
                self.logger.debug(f"{log_prefix}Link UP: {link_endpoints} already active. Active links: {len(self.topology_active_links)}")
        elif status_enum == pbft_consensus_pb2.LinkEventInfo.LINK_DOWN:
            if link_endpoints in self.topology_active_links:
                self.topology_active_links.discard(link_endpoints)
                self.logger.info(f"{log_prefix}Link DOWN: {link_endpoints} removed. Active links: {len(self.topology_active_links)}")
                self._clear_macs_for_port(dpid1_str, port1)
                self._clear_macs_for_port(dpid2_str, port2)
            else:
                self.logger.debug(f"{log_prefix}Link DOWN: {link_endpoints} already inactive. Active links: {len(self.topology_active_links)}")
        else:
            self.logger.warning(f"{log_prefix}Received unknown link status enum {status_enum} for link {link_endpoints}")
        self.logger.debug(f"{log_prefix}Current active links snapshot: {self.topology_active_links}")


    def _clear_macs_for_port(self, dpid_str, port_no):
        if dpid_str in self.mac_to_port:
            macs_to_remove = [mac for mac, learned_port in self.mac_to_port[dpid_str].items() if learned_port == port_no]
            if macs_to_remove:
                self.logger.info(f"DPID {dpid_str}: Clearing {len(macs_to_remove)} MAC entries for port {port_no} due to link down.")
                for mac in macs_to_remove:
                    del self.mac_to_port[dpid_str][mac]
                if not self.mac_to_port[dpid_str]:
                    del self.mac_to_port[dpid_str]
                    self.logger.debug(f"DPID {dpid_str}: MAC table now empty.")

# --- Replica gRPC Server Implementation ---
class PoxReplicaServicer(pbft_consensus_pb2_grpc.RyuReplicaLogicServicer):
    def CalculateAction(self, request, context):
        handler_log = core.getLogger("PBFT_POX_ReplicaHandler") # Consistent logger
        peer = context.peer()
        # Create a simple identifier for this specific CalculateAction request for logging correlation
        req_id_short = hashlib.sha256(request.packet_info.data[:64]).hexdigest()[:8] # Hash a portion of data
        log_prefix = f"[REPLICA_CALC][{req_id_short}][{time.time_ns()}] "

        handler_log.info(f"{log_prefix}Received CalculateAction from {peer} for DPID {request.packet_info.dpid}, InPort {request.packet_info.in_port}")

        computed_action_json = "{}"
        if _l2_logic:
            try:
                 action_dict = _l2_logic.compute_action(
                     request.packet_info.dpid,
                     request.packet_info.in_port,
                     request.packet_info.data,
                     packet_id_log_prefix=log_prefix # Pass prefix to logic
                 )
                 if action_dict:
                     computed_action_json = json.dumps(action_dict)
                 handler_log.debug(f"{log_prefix}Deterministic logic computed: {action_dict}")
            except Exception as e:
                handler_log.exception(f"{log_prefix}Error in deterministic logic for {peer}: {e}")
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(f"Error computing action: {e}")
                return pbft_consensus_pb2.CalculateActionResponse(computed_action=pbft_consensus_pb2.Action(action_json="{}"))
        else:
            handler_log.error(f"{log_prefix}L2 Logic not initialized!")
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            context.set_details("Replica logic unavailable")
            return pbft_consensus_pb2.CalculateActionResponse(computed_action=pbft_consensus_pb2.Action(action_json="{}"))

        response_action = pbft_consensus_pb2.Action(action_json=computed_action_json)
        handler_log.info(f"{log_prefix}Sending computed action to {peer}: {computed_action_json}")
        return pbft_consensus_pb2.CalculateActionResponse(computed_action=response_action)

    def NotifyLinkEvent(self, request, context):
        handler_log = core.getLogger("PBFT_POX_ReplicaHandler")
        peer = context.peer()
        status_name = pbft_consensus_pb2.LinkEventInfo.LinkStatus.Name(request.status)
        log_prefix = f"[REPLICA_NOTIFY_LINK][{time.time_ns()}] "
        handler_log.info(f"{log_prefix}Received NotifyLinkEvent from {peer}: "
                         f"{request.dpid1}.{request.port1} <-> {request.dpid2}.{request.port2} is {status_name}, orig_ts={request.timestamp_ns}")
        if not _l2_logic:
            handler_log.error(f"{log_prefix}L2 Logic not initialized! Cannot update link state.")
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            context.set_details("Replica logic unavailable")
            return empty_pb2.Empty()
        try:
            _l2_logic.update_link_state(
                request.dpid1, request.port1,
                request.dpid2, request.port2,
                request.status
            )
            # No need to log success here, update_link_state does it
        except Exception as e:
            handler_log.exception(f"{log_prefix}Error updating link state from {peer}: {e}")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Error updating link state: {e}")
        return empty_pb2.Empty()

# --- gRPC Server Runner ---
def _run_grpc_server(port_to_listen):
    # ... (This function can remain largely the same, just ensure loggers are consistent)
    global _grpc_server_replica
    thread_log = core.getLogger("PBFT_POX_gRPCThread")
    thread_log.info(f"Replica gRPC Server Thread: Starting on port {port_to_listen}")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    pbft_consensus_pb2_grpc.add_RyuReplicaLogicServicer_to_server(PoxReplicaServicer(), server)
    listen_addr = f'[::]:{port_to_listen}'
    server.add_insecure_port(listen_addr)
    _grpc_server_replica = server
    try:
        server.start()
        thread_log.info(f"Replica gRPC Server Thread: Listening on {listen_addr}")
        server.wait_for_termination()
    except Exception as e:
        thread_log.exception(f"Replica gRPC Server Thread: Runtime error - {e}")
    finally:
        _grpc_server_replica = None
        thread_log.info("Replica gRPC Server Thread: Stopped.")

# --- Main POX Component Logic ---
class PoxPbftApp:
    def __init__(self):
        log.info("PoxPbftApp initializing...")
        if _is_primary:
            self.connect_to_dealer()
            def register_discovery_listener():
                if core.hasComponent("openflow_discovery"):
                    # Correctly access components dictionary using the registered name
                    discovery_component = core.components.get("openflow_discovery")
                    if discovery_component:
                        discovery_component.addListenerByName(LinkEvent.__name__, self._handle_LinkEvent)
                        log.info("Primary: Registered LinkEvent listener with openflow_discovery.")
                    else: # Should not happen if hasComponent is true, but defensive
                        log.error("Primary: openflow_discovery component found by hasComponent but None via core.components.get().")
                else:
                    log.warning("Primary: openflow_discovery component not found. LinkEvent listener NOT registered.")
            core.call_when_ready(register_discovery_listener, ["openflow_discovery"])
        core.openflow.addListeners(self)
        log.info("PoxPbftApp OpenFlow listeners registered.")

    def connect_to_dealer(self):
        # ... (no changes needed here other than consistent logging if desired) ...
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
                except: pass # Best effort
            _grpc_channel_dealer = None
            _grpc_stub_dealer = None

    def _handle_ConnectionUp(self, event):
        # ... (no changes needed here other than consistent logging if desired) ...
        dpid_str = dpid_to_str(event.dpid)
        log.info(f"Switch {dpid_str} connected.")
        msg = of.ofp_flow_mod(command=of.OFPFC_ADD)
        msg.match = of.ofp_match()
        msg.actions.append(of.ofp_action_output(port = of.OFPP_CONTROLLER))
        msg.priority = 0
        event.connection.send(msg)
        log.info(f"Installed table-miss flow entry for switch {dpid_str}.")

    def _handle_LinkEvent(self, event):
        ts_link_event_start_ns = time.time_ns() # Timestamp for LinkEvent handling start
        log_prefix = f"[TOPOLOGY_PRIMARY][{ts_link_event_start_ns}] "

        dpid1_str = dpid_to_str(event.link.dpid1)
        port1 = event.link.port1
        dpid2_str = dpid_to_str(event.link.dpid2)
        port2 = event.link.port2
        status_proto = pbft_consensus_pb2.LinkEventInfo.LINK_STATUS_UNSPECIFIED
        status_log_str = "UNKNOWN"

        if event.added:
            status_proto = pbft_consensus_pb2.LinkEventInfo.LINK_UP
            status_log_str = "UP"
            log.info(f"{log_prefix}Link {status_log_str}: {dpid1_str}.{port1} <-> {dpid2_str}.{port2}")
        elif event.removed:
            status_proto = pbft_consensus_pb2.LinkEventInfo.LINK_DOWN
            status_log_str = "DOWN"
            log.info(f"{log_prefix}Link {status_log_str}: {dpid1_str}.{port1} <-> {dpid2_str}.{port2}")
        else:
            return

        if not _grpc_stub_dealer:
            log.error(f"{log_prefix}No Dealer connection to report link event {status_log_str}.")
            return

        link_event_proto = pbft_consensus_pb2.LinkEventInfo(
            dpid1=dpid1_str, port1=port1, dpid2=dpid2_str, port2=port2,
            status=status_proto, timestamp_ns=ts_link_event_start_ns # Use primary's detection time
        )

        ts_grpc_send_ns = time.time_ns()
        log.debug(f"{log_prefix}Reporting LinkEvent ({status_log_str}) to Dealer. gRPC_Send_TS={ts_grpc_send_ns}. Proto: {link_event_proto}")
        try:
            _grpc_stub_dealer.ReportLinkEvent(link_event_proto, timeout=GRPC_TIMEOUT_LINK_REPORT)
            ts_grpc_recv_ns = time.time_ns()
            grpc_latency_ms = (ts_grpc_recv_ns - ts_grpc_send_ns) / 1e6
            log.info(f"{log_prefix}Successfully reported LinkEvent ({status_log_str}) to Dealer. gRPC_Recv_TS={ts_grpc_recv_ns}. Latency_ms={grpc_latency_ms:.3f}")
        except grpc.RpcError as e_rpc:
            ts_grpc_fail_ns = time.time_ns()
            log.error(f"{log_prefix}gRPC ReportLinkEvent failed for {dpid1_str}-{dpid2_str}: {e_rpc.code()} - {e_rpc.details()}. Fail_TS={ts_grpc_fail_ns}")
        except Exception as e_gen:
            ts_grpc_fail_ns = time.time_ns()
            log.exception(f"{log_prefix}Unexpected error reporting LinkEvent for {dpid1_str}-{dpid2_str}: {e_gen}. Fail_TS={ts_grpc_fail_ns}")

    def _handle_PacketIn(self, event):
        global _packet_event_counter
        ts_packet_in_start_ns = time.time_ns() # T0

        with _packet_event_counter_lock:
            _packet_event_counter += 1
            current_event_id = _packet_event_counter
        
        # Use a consistent prefix for all logs related to this PacketIn event
        # Make it shorter by hashing some packet data if event.ofp.xid is not reliable or always 0
        packet_hash_short = hashlib.sha1(event.data[:16]).hexdigest()[:6] # Short hash of first 16 bytes
        log_prefix_pkt = f"[PKT_EVENT_{current_event_id}_{packet_hash_short}][{ts_packet_in_start_ns}] "


        if not _is_primary: return
        packet = event.parsed
        if not packet.parsed:
            log.warning(f"{log_prefix_pkt}Primary: Ignoring incompletely parsed packet.")
            return
        if packet.type == ethernet.LLDP_TYPE or packet.type == ethernet.IPV6_TYPE:
            log.debug(f"{log_prefix_pkt}Primary (DPID {dpid_to_str(event.dpid)}): Skipping LLDP/IPv6.")
            return
        if not _l2_logic:
            log.error(f"{log_prefix_pkt}Primary (DPID {dpid_to_str(event.dpid)}): L2 Logic not initialized!")
            return

        dpid_str = dpid_to_str(event.dpid)
        in_port = event.port
        
        ts_before_compute_ns = time.time_ns()
        proposed_action_dict = _l2_logic.compute_action(dpid_str, in_port, event.data, packet_id_log_prefix=log_prefix_pkt)
        ts_after_compute_ns = time.time_ns()
        compute_latency_ms = (ts_after_compute_ns - ts_before_compute_ns) / 1e6
        log.info(f"{log_prefix_pkt}Primary: L2Logic compute_action took {compute_latency_ms:.3f} ms. Proposed: {proposed_action_dict}")


        if not proposed_action_dict:
            log.info(f"{log_prefix_pkt}Primary (DPID {dpid_str}): Deterministic logic proposed no action. No consensus needed.")
            return

        proposed_action_json = json.dumps(proposed_action_dict)
        # log.info(f"{log_prefix_pkt}Primary (DPID {dpid_str}): Proposed action for PacketIn: {proposed_action_json}") # Redundant with above

        if not _grpc_stub_dealer:
            log.error(f"{log_prefix_pkt}Primary (DPID {dpid_str}): No Dealer connection. Cannot request consensus.")
            return

        ofp_msg = event.ofp
        packet_info_proto = pbft_consensus_pb2.PacketInfo(
            dpid=dpid_str, in_port=in_port,
            buffer_id=ofp_msg.buffer_id if ofp_msg.buffer_id is not None and ofp_msg.buffer_id != 0xffffffff else 0,
            total_len=ofp_msg.total_len if ofp_msg.total_len is not None else len(event.data),
            data=event.data
        )
        proposed_action_proto = pbft_consensus_pb2.Action(action_json=proposed_action_json)
        request_proto = pbft_consensus_pb2.ConsensusRequest(
            packet_info=packet_info_proto, proposed_action=proposed_action_proto
        )
        
        # Pass ts_packet_in_start_ns and log_prefix_pkt to the thread for latency calculation
        thread = threading.Thread(target=self._request_consensus_in_thread, 
                                  args=(event, request_proto, ts_packet_in_start_ns, log_prefix_pkt), 
                                  daemon=True)
        thread.start()

    def _request_consensus_in_thread(self, event, request_proto, ts_packet_in_start_ns, log_prefix_pkt): # Added params
        dpid_str = dpid_to_str(event.dpid)
        if not _grpc_stub_dealer:
            log.error(f"{log_prefix_pkt}Primary Thread (DPID {dpid_str}): Dealer stub unavailable.")
            return
        try:
            ts_grpc_send_ns = time.time_ns() # T1
            log.info(f"{log_prefix_pkt}Primary Thread (DPID {dpid_str}): Sending ConsensusRequest to Dealer. Send_TS={ts_grpc_send_ns}")
            
            response = _grpc_stub_dealer.RequestConsensus(request_proto, timeout=GRPC_TIMEOUT_PRIMARY_TO_DEALER)
            
            ts_grpc_recv_ns = time.time_ns() # T2
            dealer_roundtrip_latency_ms = (ts_grpc_recv_ns - ts_grpc_send_ns) / 1e6
            log.info(f"{log_prefix_pkt}Primary Thread (DPID {dpid_str}): Received ConsensusResponse from Dealer. Recv_TS={ts_grpc_recv_ns}. "
                     f"Dealer_RT_Latency_ms={dealer_roundtrip_latency_ms:.3f}. Consensus={response.consensus_reached}")
            
            # Pass ts_packet_in_start_ns and ts_grpc_recv_ns for further latency calculation
            core.callLater(self._execute_final_action, event, response, ts_packet_in_start_ns, ts_grpc_recv_ns, log_prefix_pkt)
        except grpc.RpcError as e_rpc:
            ts_grpc_fail_ns = time.time_ns()
            log.error(f"{log_prefix_pkt}Primary Thread (DPID {dpid_str}): gRPC ConsensusRequest failed: {e_rpc.code()} - {e_rpc.details()}. Fail_TS={ts_grpc_fail_ns}")
        except Exception as e_gen:
            ts_grpc_fail_ns = time.time_ns()
            log.exception(f"{log_prefix_pkt}Primary Thread (DPID {dpid_str}): Unexpected error during ConsensusRequest: {e_gen}. Fail_TS={ts_grpc_fail_ns}")

    def _execute_final_action(self, event, response_proto, ts_packet_in_start_ns, ts_grpc_recv_ns, log_prefix_pkt): # Added params
        ts_exec_start_ns = time.time_ns() # T3
        dpid_str = dpid_to_str(event.dpid)

        if not response_proto:
             log.warning(f"{log_prefix_pkt}Primary Execute (DPID {dpid_str}): Received None response_proto.")
             return
        if not response_proto.consensus_reached:
            log.warning(f"{log_prefix_pkt}Primary Execute (DPID {dpid_str}): Consensus not reached. Status: {getattr(response_proto, 'status_message', 'N/A')}")
            return
        if not response_proto.final_action or not response_proto.final_action.action_json:
            log.warning(f"{log_prefix_pkt}Primary Execute (DPID {dpid_str}): Consensus reached, but no final_action.")
            return

        final_action_json = response_proto.final_action.action_json
        if final_action_json == "{}" or final_action_json is None:
            log.info(f"{log_prefix_pkt}Primary Execute (DPID {dpid_str}): Received empty final_action (no-op).")
            return

        try:
            action_dict = json.loads(final_action_json)
            log.info(f"{log_prefix_pkt}Primary Execute (DPID {dpid_str}): Executing final action: {action_dict}")
            action_type = action_dict.get('type')
            ts_packet_out_sent_ns = 0 # Initialize

            if action_type == 'packet_out':
                out_port_val = action_dict.get('out_port')
                if out_port_val is None: log.error(f"{log_prefix_pkt}Execute (DPID {dpid_str}): packet_out missing 'out_port'."); return
                if isinstance(out_port_val, str) and out_port_val.upper() == 'FLOOD': out_port = of.OFPP_FLOOD
                else:
                    try: out_port = int(out_port_val)
                    except (ValueError, TypeError): log.error(f"{log_prefix_pkt}Execute (DPID {dpid_str}): Invalid out_port '{out_port_val}'."); return
                msg = of.ofp_packet_out()
                msg.actions.append(of.ofp_action_output(port=out_port))
                if event.ofp.buffer_id is not None and event.ofp.buffer_id != 0xffffffff: msg.buffer_id = event.ofp.buffer_id
                else: msg.buffer_id = 0xffffffff; msg.data = event.data
                msg.in_port = event.port
                event.connection.send(msg)
                ts_packet_out_sent_ns = time.time_ns() # T4
                log.debug(f"{log_prefix_pkt}Execute (DPID {dpid_str}): Sent PacketOut to port {out_port_val}. Sent_TS={ts_packet_out_sent_ns}")

            elif action_type == 'flow_mod':
                log.warning(f"{log_prefix_pkt}Execute (DPID {dpid_str}): FlowMod execution not fully implemented.")
                ts_packet_out_sent_ns = time.time_ns() # Or FlowMod sent time
            else:
                log.warning(f"{log_prefix_pkt}Execute (DPID {dpid_str}): Unknown action type '{action_type}'.")
                ts_packet_out_sent_ns = time.time_ns() # Fallback timestamp

            # Latency calculations
            if ts_packet_out_sent_ns > 0: # Ensure PacketOut was attempted
                pox_processing_after_grpc_ms = (ts_packet_out_sent_ns - ts_grpc_recv_ns) / 1e6
                total_e2e_latency_ms = (ts_packet_out_sent_ns - ts_packet_in_start_ns) / 1e6
                log.info(f"{log_prefix_pkt}[LATENCY_PKT] DPID {dpid_str}: "
                         f"POX_Proc_After_gRPC_ms={pox_processing_after_grpc_ms:.3f}, "
                         f"Total_E2E_Ctrl_Latency_ms={total_e2e_latency_ms:.3f}")
            else: # Only log processing up to decision point
                 pox_processing_after_grpc_ms = (ts_exec_start_ns - ts_grpc_recv_ns) / 1e6 # Time to start execution logic
                 log.info(f"{log_prefix_pkt}[LATENCY_PKT] DPID {dpid_str}: "
                         f"POX_Proc_After_gRPC_ms={pox_processing_after_grpc_ms:.3f} (action decision, no PacketOut logged)")


        except json.JSONDecodeError:
            log.error(f"{log_prefix_pkt}Primary Execute (DPID {dpid_str}): Failed to decode final_action_json: {final_action_json}")
        except Exception as e_exec:
            log.exception(f"{log_prefix_pkt}Primary Execute (DPID {dpid_str}): Error executing final_action: {e_exec}")

# --- Cleanup and Launch ---
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
        try: _grpc_server_replica.stop(grace=5.0)
        except Exception as e: log.error(f"Error stopping replica gRPC server: {e}")
        _grpc_server_replica = None
    if _grpc_server_thread and _grpc_server_thread.is_alive():
        log.info("Replica: Waiting for gRPC server thread to join...")
        _grpc_server_thread.join(timeout=5.0)
        if _grpc_server_thread.is_alive(): log.warning("Replica: gRPC server thread did not join gracefully.")
        _grpc_server_thread = None
    log.info("--- POX PBFT App Cleanup Complete ---")

def launch(primary="true", dealer_ip=None, dealer_port=None, replica_id=None):
    global _is_primary, _replica_port, _l2_logic, _pbft_dealer_endpoint, _grpc_server_thread
    if not hasattr(pbft_consensus_pb2, 'PacketInfo') or \
       not hasattr(pbft_consensus_pb2_grpc, 'PBFTConsensusStub'):
        log.critical("Failed to import generated gRPC stubs. Check paths and regeneration.")
        return
    _is_primary = primary.lower() == "true"
    _l2_logic = DeterministicL2SwitchLogic(core.getLogger("PBFT_POX_L2Logic"))
    if _is_primary:
        log.info("--- Starting PBFT POX App (Mode: PRIMARY) ---")
        if not dealer_ip or not dealer_port: log.error("Primary mode requires --dealer_ip and --dealer_port."); return
        _pbft_dealer_endpoint = f"{dealer_ip}:{dealer_port}"
        log.info(f"Primary: PBFT Dealer endpoint set to: {_pbft_dealer_endpoint}")
        core.registerNew(PoxPbftApp)
    else:
        log.info("--- Starting PBFT POX App (Mode: REPLICA) ---")
        if replica_id is None: log.error("Replica mode requires --replica_id."); return
        try:
            replica_id_int = int(replica_id)
            _replica_port = DEFAULT_REPLICA_START_PORT + replica_id_int
            log.info(f"Replica ID: {replica_id_int}, gRPC Port: {_replica_port}")
            _grpc_server_thread = threading.Thread(target=_run_grpc_server, args=(_replica_port,), daemon=True, name=f"gRPCReplicaServer-{replica_id_int}")
            _grpc_server_thread.start()
            log.info(f"Replica: gRPC server thread started for port {_replica_port}.")
        except ValueError: log.error(f"Invalid replica_id: '{replica_id}'. Must be an integer."); return
        except Exception as e: log.exception(f"Failed to start replica gRPC server thread: {e}"); return
    core.addListenerByName("GoingDownEvent", lambda event: _cleanup())
    log.info("PBFT POX Application Component Launched.")