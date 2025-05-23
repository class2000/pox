# /path/to/pox/ext/pbft_pox_app.py

import os
import json
import time
from concurrent import futures
import threading
import sys
import logging

# Third-party imports
import grpc

# --- Your generated gRPC files (ensure they are in pox/ext/) ---
import pbft_consensus_pb2
import pbft_consensus_pb2_grpc

# Import POX core and OpenFlow stuff
from pox.core import core
import pox.openflow.libopenflow_01 as of
from pox.lib.packet import ethernet
from pox.lib.packet import arp
from pox.lib.util import dpid_to_str

# Get the main logger for this component
log = core.getLogger("PBFT_POX") # Get logger BEFORE launch

# --- Configuration (Defaults, overridden by launch args) ---
DEFAULT_PBFT_DEALER_ENDPOINT = '127.0.0.1:50051'
DEFAULT_REPLICA_ENDPOINT_BASE = '0.0.0.0'
DEFAULT_REPLICA_START_PORT = 50052

# --- Global variables ---
_is_primary = True
_pbft_dealer_endpoint = DEFAULT_PBFT_DEALER_ENDPOINT
_replica_port = None

_grpc_stub_dealer = None
_grpc_channel_dealer = None
_grpc_server_replica = None
_grpc_server_thread = None
_l2_logic = None


# --- Deterministic Logic (Modified to add logging) ---
class DeterministicL2SwitchLogic:
    def __init__(self, logger):
        self.mac_to_port = {}
        self.logger = logger

    def learn_mac(self, dpid_str, src_mac_str, in_port):
        # This learning should be deterministic based ONLY on packet info
        # Log at DEBUG or lower to avoid overwhelming output during normal operation
        if dpid_str not in self.mac_to_port:
            self.mac_to_port[dpid_str] = {}
            # Use the logger passed during initialization
            self.logger.debug(f"DPID {dpid_str}: Initialized MAC table in deterministic logic.")
        if src_mac_str not in self.mac_to_port[dpid_str]:
            self.mac_to_port[dpid_str][src_mac_str] = in_port
            self.logger.debug(f"DPID {dpid_str}: Learned MAC {src_mac_str} on port {in_port} in deterministic logic.")
        elif self.mac_to_port[dpid_str][src_mac_str] != in_port:
            self.logger.debug(f"DPID {dpid_str}: MAC {src_mac_str} moved from port {self.mac_to_port[dpid_str][src_mac_str]} to {in_port} in deterministic logic.")
            self.mac_to_port[dpid_str][src_mac_str] = in_port

    def compute_action(self, dpid_str, in_port, pkt_data):
        # This method is called by both the Primary PacketIn handler and the Replica gRPC handler
        # Use the logger passed during initialization
        self.logger.debug(f"DPID {dpid_str}: Deterministic logic computing action for PacketIn on port {in_port}. Packet data length: {len(pkt_data)}")

        try:
            # Attempt to parse the packet
            pkt = ethernet(pkt_data)
        except Exception as e:
            self.logger.error(f"DPID {dpid_str}: Error parsing packet in deterministic logic: {e}")
            return None # Cannot compute action if packet is unparseable

        if not pkt.parsed:
            self.logger.debug(f"DPID {dpid_str}: Could not parse Ethernet packet in deterministic logic.")
            return None
        if pkt.type == ethernet.LLDP_TYPE or pkt.type == 0x86dd: # Ignore LLDP and IPv6
             self.logger.debug(f"DPID {dpid_str}: Skipping LLDP/IPv6 packet (type: {pkt.type:#06x}) in deterministic logic.")
             return None

        dst_mac = str(pkt.dst)
        src_mac = str(pkt.src)

        # Perform learning deterministically based on the packet
        self.learn_mac(dpid_str, src_mac, in_port)

        action = None
        if dpid_str in self.mac_to_port:
            if dst_mac in self.mac_to_port[dpid_str]:
                out_port = self.mac_to_port[dpid_str][dst_mac]
                if out_port != in_port:
                    # The deterministic action is to output to the learned port
                    action = {'type': 'packet_out', 'out_port': out_port}
                    self.logger.debug(f"DPID {dpid_str}: Deterministic logic: Known DST {dst_mac}, action packet_out to port {out_port}")
                else:
                    self.logger.debug(f"DPID {dpid_str}: Deterministic logic: DST {dst_mac} known on input port {in_port}. No packet_out action needed.")
                    action = None # No action needed if dest is on the ingress port
            else:
                # If destination is unknown, the deterministic action is to flood
                action = {'type': 'packet_out', 'out_port': of.OFPP_FLOOD}
                self.logger.debug(f"DPID {dpid_str}: Deterministic logic: Unknown DST {dst_mac}, action packet_out FLOOD.")
        else:
            # Should not happen if learn_mac is called, but as fallback:
            action = {'type': 'packet_out', 'out_port': of.OFPP_FLOOD}
            self.logger.warning(f"DPID {dpid_str}: Deterministic logic: DPID not found in MAC table! Action packet_out FLOOD.")

        self.logger.debug(f"DPID {dpid_str}: Deterministic logic returning action: {action}")
        return action


# --- Replica gRPC Server Implementation (Modified to use deterministic logic) ---
class PoxReplicaServicer(pbft_consensus_pb2_grpc.RyuReplicaLogicServicer):
    def CalculateAction(self, request, context):
        # Use a specific logger for the replica handler
        handler_log = core.getLogger("PBFT_POX_ReplicaHandler")
        peer = context.peer() # Identify the caller

        handler_log.info(f"Replica Handler: Received CalculateAction request from peer: {peer}")
        # Log request details at debug level
        handler_log.debug(f"  Request PacketInfo: DPID={request.packet_info.dpid}, InPort={request.packet_info.in_port}, BufferId={request.packet_info.buffer_id}, TotalLen={request.packet_info.total_len}, DataLen={len(request.packet_info.data)}")

        computed_action_dict = None
        computed_action_json = "{}" # Default to empty action JSON

        if _l2_logic:
            try:
                 # Call the deterministic logic using the global instance
                 computed_action_dict = _l2_logic.compute_action(
                     request.packet_info.dpid,
                     request.packet_info.in_port,
                     request.packet_info.data # Pass raw packet data
                 )
                 handler_log.debug(f"Replica Handler: Deterministic logic returned: {computed_action_dict}")

                 # Convert the result to JSON if it's not None
                 if computed_action_dict is not None:
                     computed_action_json = json.dumps(computed_action_dict)
                 else:
                     computed_action_json = "{}" # Ensure empty JSON if logic returned None

            except Exception as e:
                handler_log.exception(f"Replica Handler: Error during deterministic logic computation for peer {peer}: {e}")
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(f"Internal error during action computation: {e}")
                # Return an empty action in case of computation error
                response_action = pbft_consensus_pb2.Action(action_json="{}")
                response = pbft_consensus_pb2.CalculateActionResponse(computed_action=response_action)
                return response

        else:
            # This case should ideally not happen if launch is called correctly, but handle defensively
            handler_log.error("Replica Handler: Deterministic L2 Logic not initialized!")
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            context.set_details("Replica logic not initialized")
            # Return an empty action in case of logic not being available
            response_action = pbft_consensus_pb2.Action(action_json="{}")
            response = pbft_consensus_pb2.CalculateActionResponse(computed_action=response_action)
            return response


        handler_log.info(f"Replica Handler: Computed action JSON: {computed_action_json} for peer {peer}")

        # Create the response using the computed JSON string
        response_action = pbft_consensus_pb2.Action(action_json=computed_action_json)
        response = pbft_consensus_pb2.CalculateActionResponse(computed_action=response_action)

        handler_log.debug(f"Replica Handler: Sending response to peer {peer}.")
        return response


def _run_grpc_server(port):
    thread_log = core.getLogger("PBFT_POX_Thread")
    global _grpc_server_replica
    thread_log.info(f"Replica gRPC Server Thread: Starting on port {port}")
    try:
        # Note: max_workers=10 is usually sufficient, adjust if needed for very high load
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=20))
        pbft_consensus_pb2_grpc.add_RyuReplicaLogicServicer_to_server(
            PoxReplicaServicer(), server
        )
        listen_addr = f'[::]:{port}' # Listen on all interfaces
        server.add_insecure_port(listen_addr) # Using insecure credentials
        _grpc_server_replica = server
        server.start()
        thread_log.info(f"Replica gRPC Server Thread: Listening on {listen_addr}...")
        server.wait_for_termination() # This blocks until the server is stopped
    except Exception as e:
        # Log unexpected errors during server runtime
        thread_log.exception(f"Replica gRPC Server Thread: Failed during runtime - {e}")
    finally:
        # Ensure server is None if an error occurred or after termination
        _grpc_server_replica = None
    thread_log.info("Replica gRPC Server Thread: Server stopped.")


# --- Main POX Component Logic ---
class PoxPbftApp:
    def __init__(self):
        log.info("PoxPbftApp component initializing...")
        # _l2_logic is now initialized in launch() for both modes
        # if _is_primary: # This check is no longer strictly necessary here if _l2_logic is global
        #    _l2_logic = DeterministicL2SwitchLogic(log) # This would re-initialize, should be avoided

        if _is_primary:
            self.connect_to_dealer()
        core.openflow.addListeners(self)
        log.info("PoxPbftApp OpenFlow listeners registered.")

    def connect_to_dealer(self):
        global _grpc_channel_dealer, _grpc_stub_dealer
        if not _pbft_dealer_endpoint:
            log.error("Primary: PBFT_DEALER_ENDPOINT not configured.")
            return
        try:
            log.info(f"Primary: Attempting to connect to PBFT Dealer at {_pbft_dealer_endpoint}")
            # Use grpc.insecure_channel for non-TLS connection as per Docker Compose
            _grpc_channel_dealer = grpc.insecure_channel(_pbft_dealer_endpoint)
            # Test the channel state proactively? Or rely on the first RPC call?
            # Example check (optional):
            # try:
            #     grpc.channel_ready_future(_grpc_channel_dealer).result(timeout=5)
            #     log.info("Primary: gRPC channel to Dealer is ready.")
            # except grpc.FutureTimeoutError:
            #      log.warning("Primary: gRPC channel to Dealer timed out waiting to be ready.")
            # except Exception as e:
            #      log.error(f"Primary: Error checking gRPC channel readiness: {e}")

            _grpc_stub_dealer = pbft_consensus_pb2_grpc.PBFTConsensusStub(_grpc_channel_dealer)
            log.info(f"Primary: gRPC stub created for PBFT Dealer.")
        except Exception as e:
            log.error(f"Primary: Failed to create gRPC channel/stub to PBFT Dealer: {e}")
            # Ensure cleanup happens if initialization fails
            if _grpc_channel_dealer:
                try: _grpc_channel_dealer.close()
                except: pass
            _grpc_channel_dealer = None
            _grpc_stub_dealer = None

    def _handle_ConnectionUp(self, event):
        dpid_str = dpid_to_str(event.dpid)
        log.info(f"Switch {dpid_str} connected.")
        # Install a flow to send all packets to the controller
        msg = of.ofp_flow_mod()
        msg.actions.append(of.ofp_action_output(port = of.OFPP_CONTROLLER))
        msg.priority = 0 # Low priority, others can override
        # No match fields means it matches everything (table-miss)
        event.connection.send(msg)
        log.info(f"Installed table-miss flow entry for switch {dpid_str}")

    def _handle_PacketIn(self, event):
        # Only process PacketIn events if running as Primary
        if not _is_primary:
            log.debug("Replica: Ignoring PacketIn event (not primary)")
            return

        packet = event.parsed
        # Do basic packet validity checks before processing
        if not packet.parsed:
            log.warning("Primary: Ignoring incompletely parsed packet")
            return
        if packet.type == ethernet.LLDP_TYPE or packet.type == 0x86dd: # Ignore LLDP and IPv6
            log.debug(f"Primary (DPID {dpid_to_str(event.dpid)}): Skipping LLDP/IPv6 packet (type: {packet.type:#06x}).")
            return

        # Use the global _l2_logic instance (initialized in launch)
        if not _l2_logic:
            log.error(f"Primary (DPID {dpid_to_str(event.dpid)}): L2 Logic not initialized!")
            # Decide how to handle: Drop packet? Flood?
            # For now, just log error and stop processing this packet
            return

        dpid_str = dpid_to_str(event.dpid)
        in_port = event.port

        # Compute the *proposed* action using the deterministic logic
        # Note: The Primary also uses the deterministic logic, as it *proposes*
        # the action that *should* result from deterministic rules.
        proposed_action_dict = _l2_logic.compute_action(dpid_str, in_port, event.data)

        # If the deterministic logic says "no action needed" (e.g., dest is on ingress port)
        if not proposed_action_dict:
            log.info(f"Primary (DPID {dpid_str}): Deterministic logic proposed no action. Ignoring PacketIn for consensus.")
            # Optionally send PacketOut to drop or handle specially
            # Example to just drop (send PacketOut with no actions):
            # msg = of.ofp_packet_out(data = event.ofp) # Use original OF packet
            # # No actions list means drop
            # event.connection.send(msg)
            return

        # Prepare the gRPC request to the Dealer
        proposed_action_json = json.dumps(proposed_action_dict)
        log.info(f"Primary (DPID {dpid_str}): Proposed action: {proposed_action_json}")

        # Check if Dealer gRPC stub is available
        if not _grpc_stub_dealer:
            log.error(f"Primary (DPID {dpid_str}): No Dealer connection available to request consensus. Cannot process PacketIn.")
            # Decide how to handle: Drop packet? Flood anyway without consensus?
            # Flooding might be a reasonable fallback in some scenarios, but here we stop.
            # Example to flood (same as 'out_port': of.OFPP_FLOOD action):
            # msg = of.ofp_packet_out()
            # msg.actions.append(of.ofp_action_output(port=of.OFPP_FLOOD))
            # msg.buffer_id = event.ofp.buffer_id # Use buffer_id if possible
            # if msg.buffer_id is None or msg.buffer_id == 0xffffffff:
            #     msg.data = event.data # Or include packet data if buffer_id is invalid
            # msg.in_port = event.port
            # event.connection.send(msg)
            # log.warning(f"Primary (DPID {dpid_str}): Flooding due to no Dealer connection.")
            return # Stop processing this packet

        # Populate PacketInfo protobuf message
        ofp_msg = event.ofp
        packet_info_proto = pbft_consensus_pb2.PacketInfo(
            dpid=dpid_str,
            in_port=in_port,
            # Handle potential None or 0xffffffff for buffer_id/total_len
            buffer_id=ofp_msg.buffer_id if ofp_msg.buffer_id is not None and ofp_msg.buffer_id != 0xffffffff else 0,
            total_len=ofp_msg.total_len if ofp_msg.total_len is not None else len(event.data), # Fallback to data length if total_len is None
            data=event.data # Include the raw packet data
            # Add other fields from msg.match if needed by deterministic logic in the future
        )

        # Populate Action protobuf message for the proposed action
        proposed_action_proto = pbft_consensus_pb2.Action(action_json=proposed_action_json)

        # Create the final ConsensusRequest protobuf message
        request = pbft_consensus_pb2.ConsensusRequest(
            packet_info=packet_info_proto,
            proposed_action=proposed_action_proto
        )

        # Send the gRPC request in a separate thread to avoid blocking the POX main loop
        thread = threading.Thread(target=self._request_consensus_in_thread, args=(event, request), daemon=True)
        thread.start()

    def _request_consensus_in_thread(self, event, request):
        dpid_str = dpid_to_str(event.dpid)
        # Ensure gRPC stub is still available in the thread
        if not _grpc_stub_dealer:
            log.error(f"Primary Thread (DPID {dpid_str}): gRPC stub is None during threaded call. Cannot send request.")
            return

        try:
            log.info(f"Primary Thread (DPID {dpid_str}): Sending ConsensusRequest to Dealer...")
            start_time = time.time()
            # Set a timeout for the gRPC call
            # Use a context with timeout if needed for more granular control
            # context_timeout = 10 # seconds, matches your POX log, corresponds to Dealer's 30s wait
            response = _grpc_stub_dealer.RequestConsensus(request, timeout=30) # Use timeout parameter

            latency = time.time() - start_time
            # Log response details
            log.info(f"Primary Thread (DPID {dpid_str}): Received Response from Dealer in {latency:.4f}s. Consensus={response.consensus_reached}, Status='{response.status_message}'")

            # Schedule the execution of the final action back on the POX main thread/core loop
            core.callLater(self._execute_final_action, event, response)

        except grpc.RpcError as e:
            latency = time.time() - start_time
            # Log gRPC specific errors
            log.error(f"Primary Thread (DPID {dpid_str}): gRPC call to Dealer failed after {latency:.4f}s: Code={e.code()}, Details='{e.details()}'")
        except Exception as e:
            latency = time.time() - start_time
            # Log any other unexpected errors
            log.exception(f"Primary Thread (DPID {dpid_str}): Unexpected error during gRPC call: {e}")

    def _execute_final_action(self, event, response):
        dpid_str = dpid_to_str(event.dpid)
        # Validate the response received from the Dealer
        if not response:
             log.warning(f"Primary Execute (DPID {dpid_str}): Received None response from Dealer.")
             return
        if not response.consensus_reached:
            log.warning(f"Primary Execute (DPID {dpid_str}): Consensus not reached according to Dealer. Status: {getattr(response, 'status_message', 'N/A')}")
            # Decide how to handle non-consensus: Drop packet? Re-initiate?
            return
        # Check if final_action and its content are present
        if not response.final_action or not response.final_action.action_json:
            log.warning(f"Primary Execute (DPID {dpid_str}): Consensus reached, but no final action provided in response.")
            return

        # Execute the action agreed upon by consensus
        final_action_json = response.final_action.action_json

        # Handle the empty action case explicitly (often means drop or no-op)
        if final_action_json == "{}" or final_action_json is None: # Check for None defensively
            log.info(f"Primary Execute (DPID {dpid_str}): Received empty final action JSON (no-op).")
            # Depending on desired behavior, might explicitly send a drop packet_out here
            return

        try:
            # Parse the action JSON string
            action_dict = json.loads(final_action_json)
            log.info(f"Primary Execute (DPID {dpid_str}): Executing final action: {action_dict}")

            # Process the action dictionary
            action_type = action_dict.get('type')

            if action_type == 'packet_out':
                # Build and send PacketOut message
                out_port_val = action_dict.get('out_port')
                if out_port_val is None:
                    log.error(f"Execute (DPID {dpid_str}): packet_out action missing 'out_port'.")
                    return

                # Convert out_port value (could be int or string like "FLOOD")
                if isinstance(out_port_val, str) and out_port_val.upper() == 'FLOOD':
                    out_port = of.OFPP_FLOOD
                else:
                    try:
                        out_port = int(out_port_val)
                    except (ValueError, TypeError):
                        log.error(f"Execute (DPID {dpid_str}): Invalid out_port value '{out_port_val}'. Must be int or 'FLOOD'.")
                        return

                msg = of.ofp_packet_out()
                msg.actions.append(of.ofp_action_output(port=out_port))

                # Use the original buffer_id if it's valid, otherwise include packet data
                if event.ofp.buffer_id is not None and event.ofp.buffer_id != 0xffffffff:
                    msg.buffer_id = event.ofp.buffer_id
                    msg.data = None # Clear data if using buffer_id
                else:
                    msg.buffer_id = 0xffffffff # Indicate no buffer_id
                    msg.data = event.data # Include original packet data

                msg.in_port = event.port # The port the packet came in on

                # Send the constructed OpenFlow message to the switch connection
                event.connection.send(msg)
                log.debug(f"Execute (DPID {dpid_str}): Sent PacketOut to port {out_port_val}")

            elif action_type == 'flow_mod':
                log.warning(f"Execute (DPID {dpid_str}): FlowMod execution is not yet implemented.")
                # Implement logic to build and send of.ofp_flow_mod message
                pass # Placeholder

            else:
                log.warning(f"Execute (DPID {dpid_str}): Received unknown action type '{action_type}'")

        except json.JSONDecodeError:
            # Log errors during JSON parsing
            log.error(f"Primary Execute (DPID {dpid_str}): Failed to decode final action JSON: {final_action_json}")
        except Exception as e_exec:
            # Log any other errors during action execution
            log.exception(f"Primary Execute (DPID {dpid_str}): Error during action execution: {e_exec}")


def _cleanup():
    log.info("--- Stopping PBFT POX App ---")
    # Close the gRPC channel to the Dealer if it exists
    if _grpc_channel_dealer:
        log.info("Primary: Closing gRPC channel to PBFT Dealer.")
        try: _grpc_channel_dealer.close()
        except Exception as e: log.error(f"Error closing dealer channel: {e}")
    # Stop the Replica gRPC server if it exists
    if _grpc_server_replica:
        log.info("Replica: Stopping gRPC server...")
        # stop() is non-blocking, wait() is needed for graceful shutdown
        # Or use stop(grace_period)
        try: _grpc_server_replica.stop(5) # Give 5 seconds for graceful shutdown
        except Exception as e: log.error(f"Error stopping replica server: {e}")
        # Wait for the server thread to finish if it was started
        if _grpc_server_thread and _grpc_server_thread.is_alive():
             log.debug("Waiting for gRPC server thread to join...")
             _grpc_server_thread.join(5) # Wait up to 5 seconds for the thread
             if _grpc_server_thread.is_alive():
                  log.warning("gRPC server thread did not join gracefully.")


# --- launch() function ---
def launch(primary="true", dealer_ip=None, dealer_port=None, replica_id=None):
    global _is_primary, _replica_port, _l2_logic, _pbft_dealer_endpoint, _grpc_server_thread

    _is_primary = primary.lower() == "true"
    # Initialize the deterministic logic instance. It's used by BOTH primary and replicas.
    # Pass the main logger to the logic class.
    _l2_logic = DeterministicL2SwitchLogic(log)
    log.info("DeterministicL2SwitchLogic initialized.")

    # Setup based on primary or replica mode
    if _is_primary:
        log.info("--- Starting PBFT POX App (Mode: PRIMARY) ---")
        # Primary requires Dealer connection details
        if not dealer_ip or not dealer_port:
            log.error("Primary mode requires --dealer_ip and --dealer_port arguments.")
            return # Exit launch if required args are missing
        _pbft_dealer_endpoint = f"{dealer_ip}:{dealer_port}"
        log.info(f"Dealer endpoint set to: {_pbft_dealer_endpoint}")

        # Register the main application component
        core.registerNew(PoxPbftApp)

    else: # Replica mode
        log.info("--- Starting PBFT POX App (Mode: REPLICA) ---")
        # Replica requires an ID to determine its port
        if replica_id is None:
            log.error("Replica mode requires --replica_id argument.")
            return # Exit launch if required args are missing
        try:
            # Calculate the replica's gRPC listening port based on its ID
            replica_id_int = int(replica_id)
            _replica_port = DEFAULT_REPLICA_START_PORT + replica_id_int
            log.info(f"Replica ID: {replica_id_int}, gRPC Port: {_replica_port}")

            # Start the gRPC server for replicas in a separate thread
            # Use a daemon thread so it doesn't prevent POX from exiting
            _grpc_server_thread = threading.Thread(
                target=_run_grpc_server,
                args=(_replica_port,),
                daemon=True,
                name=f"gRPCReplicaServer-{replica_id_int}" # Give the thread a name for easier debugging
            )
            _grpc_server_thread.start()
            log.info(f"Replica gRPC server thread started (Port: {_replica_port}).")

        except ValueError:
            log.error(f"Invalid replica_id: '{replica_id}'. Must be an integer.")
            return # Exit launch if ID is invalid
        except Exception as e:
            log.exception(f"Failed to start replica gRPC server thread: {e}")
            return # Exit launch on thread start error

    # Add a listener for POX GoingDownEvent to perform cleanup
    core.addListenerByName("GoingDownEvent", lambda event: _cleanup())
    log.info("PBFT POX Application Component Launched.")