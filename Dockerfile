# Use a specific Python 3.10 slim image as a base
FROM python:3.10-slim

# Set working directory
WORKDIR /pox_root

# Install git to clone POX and potentially other dependencies if needed
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    && rm -rf /var/lib/apt/lists/*

# Clone the POX repository
RUN git clone https://github.com/noxrepo/pox.git

# Install Python dependencies for gRPC and protobuf
# Make sure versions are compatible if needed, but defaults often work
RUN pip install --no-cache-dir grpcio grpcio-tools protobuf

# Set the working directory to the POX folder
WORKDIR /pox_root/pox

# Copy your custom component and generated proto files into the ext directory
# Adjust source paths if your files are located differently relative to the Dockerfile
COPY ./pox/ext/pbft_pox_app.py ./ext/
COPY ./pox/ext/pbft_consensus_pb2.py ./ext/
# --- CORRECTED PATH BELOW ---
COPY ./pox/ext/pbft_consensus_pb2_grpc.py ./ext/
# Add any other custom files needed by your POX component here

# Default command (can be overridden in docker-compose)
# Expose the default POX OpenFlow port (optional, depends if external switches connect)
# EXPOSE 6633
# Expose the default replica start port range (adjust if needed)
# EXPOSE 50052-50055

# The CMD will be specified in docker-compose.yml
CMD ["./pox.py"]
