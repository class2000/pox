import re
import pandas as pd
import matplotlib.pyplot as plt
import numpy as np
import os
from collections import defaultdict
import seaborn as sns # For better looking plots
import math # For floor function
from scipy import stats # For ANOVA

# --- Configuration ---
# Directory containing your .txt data files (rttX.txt and Xnodes.txt)
# If the script is in the same directory as the data files, use '.'
DATA_DIRECTORY = '.' 
OUTPUT_DIR = 'analysis_plots' # Directory to save plots

# --- Helper Functions ---

def parse_ping_line(line):
    """
    Parses a single line of ping output to extract icmp_seq and time.
    Example line: "64 bytes from 10.0.0.2: icmp_seq=1 ttl=64 time=85.5 ms"
    """
    match = re.search(r'icmp_seq=(\d+).*time=([\d.]+) ms', line)
    if match:
        icmp_seq = int(match.group(1))
        rtt_time = float(match.group(2))
        return icmp_seq, rtt_time
    return None, None

def extract_rtt_from_file_content(content, is_rtt_file=True):
    """
    Extracts RTT data from the string content of a file.
    For rttX.txt files, returns a DataFrame of (icmp_seq, time).
    For Xnodes.txt files, returns a single time value.
    """
    rtt_data_list = []
    lines = content.strip().split('\n')
    if not lines:
        return None

    if is_rtt_file: # e.g., rtt4.txt, rtt6.txt, rtt8.txt
        for line in lines:
            if line.strip(): 
                icmp_seq, rtt_time = parse_ping_line(line)
                if rtt_time is not None:
                    rtt_data_list.append({'icmp_seq': icmp_seq, 'time': rtt_time})
        return pd.DataFrame(rtt_data_list) if rtt_data_list else pd.DataFrame()
    else: # e.g., 4nodes.txt, 13nodes.txt
        if lines and lines[0].strip(): 
            _, rtt_time = parse_ping_line(lines[0])
            return rtt_time
    return None


def get_node_count_from_filename(filename):
    """Extracts the number of nodes from filenames like 'rtt4.txt' or '4nodes.txt'."""
    match = re.search(r'(\d+)', filename)
    if match:
        return int(match.group(1))
    return None

def calculate_fault_tolerance(n_nodes):
    """Calculates the number of Byzantine faults (f) tolerated for n_nodes."""
    if n_nodes < 1:
        return 0
    return math.floor((n_nodes - 1) / 3)

def load_data_from_filesystem(data_dir):
    """
    Loads and parses all RTT data from .txt files in the specified directory.
    """
    parsed_data = {
        'rtt_series': {},  # For rttX.txt files (multiple pings)
        'first_ping': {}   # For Xnodes.txt files (single first ping)
    }

    if not os.path.isdir(data_dir):
        print(f"Error: Data directory '{data_dir}' not found.")
        return None

    print(f"Scanning directory: {os.path.abspath(data_dir)}")
    found_files = False
    for filename in os.listdir(data_dir):
        file_path = os.path.join(data_dir, filename)
        if not os.path.isfile(file_path):
            continue

        node_count = get_node_count_from_filename(filename)
        if node_count is None:
            continue
        
        found_files = True
        try:
            with open(file_path, 'r', encoding='utf-8') as f: # Added encoding
                content = f.read()
        except Exception as e:
            print(f"Error reading file {file_path}: {e}")
            continue

        if filename.startswith('rtt') and filename.endswith('.txt'):
            df = extract_rtt_from_file_content(content, is_rtt_file=True)
            if df is not None and not df.empty:
                parsed_data['rtt_series'][node_count] = df
                print(f"Successfully parsed {filename} ({len(df)} RTT entries)")
            else:
                print(f"Warning: No data parsed from {filename} or data was empty.")
        elif 'nodes.txt' in filename: 
            rtt_time = extract_rtt_from_file_content(content, is_rtt_file=False)
            if rtt_time is not None:
                parsed_data['first_ping'][node_count] = rtt_time
                print(f"Successfully parsed {filename} (First ping RTT: {rtt_time} ms)")
            else:
                print(f"Warning: No data parsed from {filename}")
            
    if not found_files:
        print(f"Warning: No files matching 'rtt*.txt' or '*nodes.txt' found in {data_dir}")

    if parsed_data['first_ping']:
        parsed_data['first_ping'] = dict(sorted(parsed_data['first_ping'].items()))
    if parsed_data['rtt_series']:
        parsed_data['rtt_series'] = dict(sorted(parsed_data['rtt_series'].items()))
    
    return parsed_data

# --- Analysis Functions ---

def analyze_arp_effect(data):
    """
    Analyzes the RTT of the first ping (likely including ARP) versus subsequent pings.
    Provides an in-depth textual summary of the findings, mirroring the provided Markdown.
    """
    print("\n\n--- In-depth ARP Effect Analysis ---")
    arp_analysis_results = {}
    report_lines = ["**ARP Overhead and First Ping RTT Analysis**\n"]

    if not data or not data.get('rtt_series'):
        no_data_msg = "No 'rtt_series' data (from rttX.txt files) available for ARP analysis. Cannot proceed with this section."
        print(no_data_msg)
        report_lines.append(no_data_msg)
        return arp_analysis_results, "\n".join(report_lines)

    # Using specific node counts mentioned in the provided Markdown for detailed breakdown
    node_counts_for_detailed_arp = [4, 6, 8] 

    for nodes in node_counts_for_detailed_arp:
        df = data['rtt_series'].get(nodes) # Get data for specific node count
        
        current_report = [f"\n* **{nodes} PBFT Nodes (rtt{nodes}.txt & {nodes}nodes.txt data):**"]
        
        if df is None or df.empty:
            skip_msg = f"Skipping ARP analysis for {nodes} nodes setup (rtt{nodes}.txt): No RTT data found."
            print(skip_msg)
            current_report.append(f"    * {skip_msg}")
            report_lines.extend(current_report)
            arp_analysis_results[nodes] = {} # Add empty entry
            continue

        first_pings_df = df[df['icmp_seq'] == 1]
        subsequent_pings_df = df[df['icmp_seq'] > 1]
        
        avg_first_ping_rtt_series = first_pings_df['time'].mean() if not first_pings_df.empty else np.nan
        std_first_ping_rtt_series = first_pings_df['time'].std() if not first_pings_df.empty else np.nan
        
        avg_subsequent_ping_rtt_series = subsequent_pings_df['time'].mean() if not subsequent_pings_df.empty else np.nan
        # std_subsequent_ping_rtt_series = subsequent_pings_df['time'].std() if not subsequent_pings_df.empty else np.nan # Not in provided MD
        
        first_ping_single_shot = data.get('first_ping', {}).get(nodes, np.nan)
        
        if not first_pings_df.empty:
            current_report.append(f"    * **First Pings (icmp_seq=1 from rtt{nodes}.txt):**")
            current_report.append(f"        * Samples: {len(first_pings_df)}")
            current_report.append(f"        * Average RTT: {avg_first_ping_rtt_series:.2f} ms")
            current_report.append(f"        * Std. Dev.: {std_first_ping_rtt_series:.2f} ms")
        else:
            current_report.append(f"    * No 'icmp_seq=1' pings found in rtt{nodes}.txt.")

        if not np.isnan(first_ping_single_shot):
            current_report.append(f"    * **Single First Ping (from {nodes}nodes.txt):** {first_ping_single_shot:.2f} ms")
            if not np.isnan(avg_first_ping_rtt_series):
                diff_single_vs_avg_first = first_ping_single_shot - avg_first_ping_rtt_series
                current_report.append(f"        * Difference from rtt{nodes}.txt avg first ping: {diff_single_vs_avg_first:.2f} ms")
        else:
            current_report.append(f"    * Single First Ping data (from {nodes}nodes.txt): Not available.")
            
        if not subsequent_pings_df.empty:
            current_report.append(f"    * **Subsequent Pings (icmp_seq > 1 from rtt{nodes}.txt):**")
            current_report.append(f"        * Samples: {len(subsequent_pings_df)}")
            current_report.append(f"        * Average RTT: {avg_subsequent_ping_rtt_series:.2f} ms")
            # The provided MD doesn't list std for subsequent here, so commenting out:
            # current_report.append(f"        * Std. Dev.: {std_subsequent_ping_rtt_series:.2f} ms") 
        else:
            current_report.append(f"    * No subsequent pings (icmp_seq > 1) found in rtt{nodes}.txt.")

        arp_overhead_estimate_series = np.nan
        if not np.isnan(avg_first_ping_rtt_series) and not np.isnan(avg_subsequent_ping_rtt_series):
            arp_overhead_estimate_series = avg_first_ping_rtt_series - avg_subsequent_ping_rtt_series
            current_report.append(f"    * **Estimated ARP Overhead (rtt{nodes}.txt: avg_first - avg_subsequent):** {arp_overhead_estimate_series:.2f} ms")
        else:
            current_report.append(f"    * Estimated ARP Overhead (based on rtt{nodes}.txt): Not enough data to calculate.")

        arp_overhead_estimate_single = np.nan
        if not np.isnan(first_ping_single_shot) and not np.isnan(avg_subsequent_ping_rtt_series):
            arp_overhead_estimate_single = first_ping_single_shot - avg_subsequent_ping_rtt_series
            current_report.append(f"    * **Estimated ARP Overhead ({nodes}nodes.txt vs rtt{nodes}.txt avg_subsequent):** {arp_overhead_estimate_single:.2f} ms")
        else:
             current_report.append(f"    * Estimated ARP Overhead (based on {nodes}nodes.txt): Not enough data to calculate.")
        
        print("\n".join(current_report)) # Print to console for immediate feedback
        report_lines.extend(current_report)
            
        arp_analysis_results[nodes] = {
            'avg_first_ping_rtt_series': avg_first_ping_rtt_series,
            'std_first_ping_rtt_series': std_first_ping_rtt_series,
            'avg_subsequent_ping_rtt_series': avg_subsequent_ping_rtt_series,
            'first_ping_single_shot': first_ping_single_shot,
            'arp_overhead_estimate_series': arp_overhead_estimate_series,
            'arp_overhead_estimate_single': arp_overhead_estimate_single,
            'num_first_pings_series': len(first_pings_df),
            'num_subsequent_pings_series': len(subsequent_pings_df)
        }
    
    report_lines.append("\n**Summary of ARP Effect:**\n")
    if not arp_analysis_results or all(not res for res in arp_analysis_results.values()): # Check if all results are empty
        report_lines.append("Insufficient data to summarize ARP effect.\n")
    else:
        # Filter out NaN before calculating mean
        avg_overheads_series_valid = [res['arp_overhead_estimate_series'] for res in arp_analysis_results.values() if res and not np.isnan(res.get('arp_overhead_estimate_series', np.nan))]
        avg_overheads_single_valid = [res['arp_overhead_estimate_single'] for res in arp_analysis_results.values() if res and not np.isnan(res.get('arp_overhead_estimate_single', np.nan))]

        if avg_overheads_series_valid:
            report_lines.append(f"- The average estimated ARP overhead (from rttX.txt data) across configurations with available data is: {np.mean(avg_overheads_series_valid):.2f} ms.")
        else:
            report_lines.append("- Could not calculate average ARP overhead from rttX.txt data due to missing values.")
        
        if avg_overheads_single_valid:
             report_lines.append(f"- The average estimated ARP overhead (comparing Xnodes.txt with rttX.txt subsequent pings) across configurations with available data is: {np.mean(avg_overheads_single_valid):.2f} ms.")
        else:
            report_lines.append("- Could not calculate average ARP overhead from Xnodes.txt data due to missing values.")

        report_lines.append("Generally, the first ping RTT is higher than subsequent pings, likely due to ARP resolution or initial cache misses in the network path or PBFT system. The magnitude of this difference is quantified above for each node configuration.")
    
    return arp_analysis_results, "\n".join(report_lines)


def analyze_rtt_scaling(data, arp_results):
    """
    Analyzes how RTT scales with the number of PBFT nodes and faults tolerated.
    Provides an in-depth textual summary of the findings.
    """
    print("\n\n--- In-depth RTT Scaling with Network Size & Fault Tolerance ---")
    report_lines = ["\n**RTT Scaling with Network Size and Fault Tolerance Analysis**\n"]
    
    if not data:
        no_data_msg = "No data available for RTT scaling analysis."
        print(no_data_msg)
        report_lines.append(no_data_msg)
        return "\n".join(report_lines)

    nodes_fp_keys, rtt_fp_values, faults_fp = [], [], []
    if data.get('first_ping'):
        for n, rtt in sorted(data['first_ping'].items()):
            nodes_fp_keys.append(n)
            rtt_fp_values.append(rtt)
            faults_fp.append(calculate_fault_tolerance(n))

    nodes_rtt_series_subsequent_keys, rtt_rtt_series_subsequent_values, faults_subsequent = [], [], []
    avg_first_ping_series_values, faults_first_series, temp_nodes_for_first_series = [], [], []
    
    if arp_results:
        valid_arp_results = {k: v for k, v in sorted(arp_results.items()) if v is not None}
        for n, res in valid_arp_results.items():
            if not np.isnan(res.get('avg_subsequent_ping_rtt_series')):
                nodes_rtt_series_subsequent_keys.append(n)
                rtt_rtt_series_subsequent_values.append(res['avg_subsequent_ping_rtt_series'])
                faults_subsequent.append(calculate_fault_tolerance(n))
            
            if not np.isnan(res.get('avg_first_ping_rtt_series')):
                avg_first_ping_series_values.append(res['avg_first_ping_rtt_series'])
                temp_nodes_for_first_series.append(n)
        faults_first_series = [calculate_fault_tolerance(n) for n in temp_nodes_for_first_series]

    report_lines.append("This section examines how ICMP Round-Trip Time (RTT) changes with:\n"
                        "1.  The total number of PBFT nodes in the network.\n"
                        "2.  The number of Byzantine faults (`f`) the network can tolerate (`f = floor((N-1)/3)`).\n"
                        "Metrics plotted:\n"
                        "-   **First Ping RTT (Xnodes.txt):** RTT of a single, initial ICMP request.\n"
                        "-   **Avg. Subsequent Ping RTT (rttX.txt):** Average RTT of pings with `icmp_seq > 1`.\n"
                        "-   **Avg. First Ping RTT (rttX.txt):** Average RTT of pings with `icmp_seq = 1`.\n")

    plt.figure(figsize=(14, 8))
    if nodes_fp_keys:
        plt.plot(nodes_fp_keys, rtt_fp_values, marker='o', linestyle='-', color='blue', label='First Ping RTT (Xnodes.txt)')
    
    if nodes_rtt_series_subsequent_keys:
        plt.plot(nodes_rtt_series_subsequent_keys, rtt_rtt_series_subsequent_values, marker='s', linestyle='--', color='green', label='Avg. Subsequent Ping RTT (rttX.txt, icmp_seq > 1)')
        if temp_nodes_for_first_series and avg_first_ping_series_values: # Check if both have data
             plt.plot(temp_nodes_for_first_series, avg_first_ping_series_values, marker='^', linestyle=':', color='red', label='Avg. First Ping RTT (rttX.txt, icmp_seq = 1)')

    all_node_counts_for_xticks = sorted(list(set(nodes_fp_keys + nodes_rtt_series_subsequent_keys + temp_nodes_for_first_series)))
    if all_node_counts_for_xticks:
        plt.xticks(all_node_counts_for_xticks, [str(n) for n in all_node_counts_for_xticks], rotation=45, ha="right")
    
    plt.xlabel("Number of PBFT Nodes (N)")
    plt.ylabel("Round-Trip Time (ms)")
    plt.title("ICMP RTT Scaling with Number of PBFT Nodes")
    plt.legend()
    plt.grid(True, which="both", ls="--")
    plt.tight_layout()
    if not os.path.exists(OUTPUT_DIR): os.makedirs(OUTPUT_DIR)
    plot_path_nodes = os.path.join(OUTPUT_DIR, "rtt_scaling_vs_nodes.png")
    plt.savefig(plot_path_nodes)
    print(f"\nSaved RTT scaling vs. Nodes plot to {plot_path_nodes}")
    report_lines.append(f"\n**Observations from RTT vs. Number of Nodes Plot ({os.path.basename(plot_path_nodes)}):**\n")
    # Placeholder for user's specific observations from the plot
    report_lines.append("The plot visually demonstrates the trend of RTT as the network size increases. Key observations include:\n")
    if len(nodes_fp_keys) > 1:
        slope_fp, intercept_fp = np.polyfit(nodes_fp_keys, rtt_fp_values, 1)
        report_lines.append(f"* For First Ping RTT (Xnodes.txt): The RTT generally increases with the number of nodes. A linear fit suggests an increase of approximately {slope_fp:.2f} ms per additional node (intercept: {intercept_fp:.2f} ms).")
    if len(nodes_rtt_series_subsequent_keys) > 1 and len(rtt_rtt_series_subsequent_values) > 1: # Check for sufficient data for polyfit
        slope_subsequent, intercept_subsequent = np.polyfit(nodes_rtt_series_subsequent_keys, rtt_rtt_series_subsequent_values, 1)
        report_lines.append(f"* For Average Subsequent Ping RTT (rttX.txt): This metric also tends to increase with network size. A linear fit indicates an increase of roughly {slope_subsequent:.2f} ms per additional node (intercept: {intercept_subsequent:.2f} ms).")
    report_lines.append("* The gap between 'First Ping RTT' and 'Avg. Subsequent Ping RTT' typically represents the initial overheads (ARP, cache effects). This gap may also change with network size.")
    report_lines.append("* The rate of increase (slope) can indicate how well the PBFT consensus and network communication scale in terms of latency for simple ICMP requests. A steeper slope implies higher latency cost per node.")
    report_lines.append("* Any significant jumps or non-linearities in the plot might point to specific node counts where performance characteristics change, possibly due to resource limits, network topology effects, or PBFT protocol overheads becoming more pronounced.")


    plt.figure(figsize=(14, 8))
    if faults_fp:
        df_fp_faults = pd.DataFrame({'faults': faults_fp, 'rtt': rtt_fp_values, 'nodes': nodes_fp_keys})
        plt.plot(df_fp_faults['faults'], df_fp_faults['rtt'], marker='o', linestyle='-', color='blue', label='First Ping RTT (Xnodes.txt) vs Faults')
        for i, txt in enumerate(df_fp_faults['nodes']): 
            plt.annotate(f"N={txt}", (df_fp_faults['faults'].iloc[i], df_fp_faults['rtt'].iloc[i]), textcoords="offset points", xytext=(0,5), ha='center', fontsize=8)

    if faults_subsequent:
        df_subsequent_faults = pd.DataFrame({'faults': faults_subsequent, 'rtt': rtt_rtt_series_subsequent_values, 'nodes': nodes_rtt_series_subsequent_keys})
        plt.plot(df_subsequent_faults['faults'], df_subsequent_faults['rtt'], marker='s', linestyle='--', color='green', label='Avg. Subsequent Ping RTT (rttX.txt) vs Faults')
        for i, txt in enumerate(df_subsequent_faults['nodes']): 
             plt.annotate(f"N={txt}", (df_subsequent_faults['faults'].iloc[i], df_subsequent_faults['rtt'].iloc[i]), textcoords="offset points", xytext=(0,5), ha='center', fontsize=8)
    
    if faults_first_series and len(avg_first_ping_series_values) == len(faults_first_series):
        df_first_series_faults = pd.DataFrame({'faults': faults_first_series, 
                                               'rtt': avg_first_ping_series_values,
                                               'nodes': temp_nodes_for_first_series}) 
        plt.plot(df_first_series_faults['faults'], df_first_series_faults['rtt'], marker='^', linestyle=':', color='red', label='Avg. First Ping RTT (rttX.txt) vs Faults')
        for i, txt in enumerate(df_first_series_faults['nodes']): 
             plt.annotate(f"N={txt}", (df_first_series_faults['faults'].iloc[i], df_first_series_faults['rtt'].iloc[i]), textcoords="offset points", xytext=(0,5), ha='center', fontsize=8)

    all_fault_counts = sorted(list(set(faults_fp + faults_subsequent + faults_first_series)))
    if all_fault_counts:
        max_f = max(all_fault_counts) if all_fault_counts else 0
        # Ensure ticks are meaningful and cover the range of observed fault values
        xtick_values = sorted(list(set(all_fault_counts))) 
        if not xtick_values: xtick_values = [0] # Default if no fault data
        plt.xticks(xtick_values, [str(f) for f in xtick_values])

    plt.xlabel("Number of Tolerated Byzantine Faults (f)")
    plt.ylabel("Round-Trip Time (ms)")
    plt.title("ICMP RTT Scaling with PBFT Fault Tolerance")
    plt.legend()
    plt.grid(True, which="both", ls="--")
    plt.tight_layout()
    plot_path_faults = os.path.join(OUTPUT_DIR, "rtt_scaling_vs_faults_tolerated.png")
    plt.savefig(plot_path_faults)
    print(f"\nSaved RTT scaling vs. Faults Tolerated plot to {plot_path_faults}")
    report_lines.append(f"\n**Observations from RTT vs. Faults Tolerated Plot ({os.path.basename(plot_path_faults)}):**\n")
    report_lines.append("This plot shows RTT against the number of Byzantine faults the system can tolerate. Since `f` increases in steps as `N` increases (e.g., N=4-6 means f=1), the plot might show plateaus or steps. It helps understand if increasing fault tolerance (which requires more nodes) comes with a proportional or disproportional RTT cost.\n")
    
    report_lines.append("\n**PBFT Fault Tolerance Reference:**\n")
    report_lines.append("| Number of Nodes (N) | Byzantine Faults Tolerated (f) |")
    report_lines.append("|---------------------|--------------------------------|")
    fault_map_display = {
        (1, 3): 0, (4, 6): 1, (7, 9): 2, (10, 12): 3, (13, 15): 4,
        (16, 18): 5, (19, 21): 6, (22, 24): 7, (25, 27): 8, (28, 30): 9,
        (31, 33): 10, (34, 36): 11, (37, 39): 12, (40, 42): 13, (43, 43): 14
    }
    for node_range, faults_val in fault_map_display.items():
        if node_range[0] == node_range[1]:
            report_lines.append(f"| {node_range[0]}                 | {faults_val}                             |")
        else:
            report_lines.append(f"| {node_range[0]} to {node_range[1]}            | {faults_val}                             |")
    
    return "\n".join(report_lines)


def plot_overlaid_rtt_distributions(data):
    """
    Plots overlaid RTT distributions (e.g., violin plots) for rttX.txt datasets.
    """
    print("\n\n--- Overlaying RTT Distributions (from rttX.txt files) ---")
    report_lines = ["\n**Overlaid RTT Distribution Analysis (from rttX.txt files)**\n"]
    report_lines.append("This plot compares the RTT distributions for different network sizes (4, 6, and 8 nodes) directly using violin plots. It helps visualize changes in median, spread, and shape of the RTT distribution as the network scales for these specific configurations.\n")

    if not data or not data.get('rtt_series'):
        no_data_msg = "No 'rtt_series' data available for overlaid distribution plot."
        print(no_data_msg)
        report_lines.append(no_data_msg)
        return "\n".join(report_lines)

    plot_data_list = []
    target_node_counts = [4, 6, 8]
    
    for nodes in target_node_counts:
        df = data['rtt_series'].get(nodes)
        if df is not None and not df.empty and 'time' in df.columns:
            temp_df = df[['time']].copy()
            temp_df['Nodes'] = f'{nodes} Nodes'
            plot_data_list.append(temp_df)
        else:
            print(f"Warning: No rtt_series data for {nodes} nodes for overlaid distribution plot.")

    if not plot_data_list:
        no_plot_data_msg = "Insufficient data for 4, 6, or 8 nodes in 'rtt_series' to generate overlaid distribution plot."
        print(no_plot_data_msg)
        report_lines.append(no_plot_data_msg)
        return "\n".join(report_lines)

    combined_df = pd.concat(plot_data_list)
    combined_df['Nodes'] = pd.Categorical(combined_df['Nodes'], categories=[f'{n} Nodes' for n in target_node_counts if f'{n} Nodes' in combined_df['Nodes'].unique()], ordered=True)

    plt.figure(figsize=(10, 7))
    sns.violinplot(x='Nodes', y='time', data=combined_df, palette="pastel", inner="quartile", cut=0) 
    plt.title("Overlayed RTT Distributions (All Pings from rttX.txt for 4, 6, 8 Nodes)")
    plt.xlabel("PBFT Network Size")
    plt.ylabel("RTT (ms)")
    plt.grid(True, axis='y', ls='--')
    plt.tight_layout()
    
    if not os.path.exists(OUTPUT_DIR): os.makedirs(OUTPUT_DIR)
    plot_path = os.path.join(OUTPUT_DIR, "overlaid_rtt_distributions_4_6_8_nodes.png")
    plt.savefig(plot_path)
    print(f"Saved overlaid RTT distributions plot to {plot_path}")
    report_lines.append(f"* *Overlaid Distributions Plot:* Saved as `{os.path.basename(plot_path)}`. "
                        "Violin plots show the density of RTT data. Wider sections indicate more data points at that RTT. The white dot is the median, the thick black bar is the IQR, and thin lines are whiskers.")
    report_lines.append("* This visualization allows for a quick comparison of how the overall RTT characteristics (central tendency, spread, density) evolve as the number of nodes changes from 4 to 6 to 8.")
    
    return "\n".join(report_lines)

def plot_overlaid_scatter_rtt(data):
    """
    Plots all RTT data points from rttX.txt (for 4, 6, 8 nodes) on a single scatter plot,
    with jitter for better visibility.
    """
    print("\n\n--- Overlaying All RTT Data Points (Scatter Plot from rttX.txt) ---")
    report_lines = ["\n**Overlayed Scatter Plot of All RTT Data Points (from rttX.txt files)**\n"]
    report_lines.append("This scatter plot displays all individual RTT measurements from the `rttX.txt` files for 4, 6, and 8 node configurations. Jitter is added to the x-axis to reduce overplotting and better visualize the density and spread of data points for each category.\n")

    if not data or not data.get('rtt_series'):
        no_data_msg = "No 'rtt_series' data available for overlaid scatter plot."
        print(no_data_msg)
        report_lines.append(no_data_msg)
        return "\n".join(report_lines)

    plt.figure(figsize=(12, 8))
    
    colors = ['blue', 'green', 'red']
    node_configs_to_plot = [4, 6, 8] 
    
    plot_exists = False
    for i, nodes in enumerate(node_configs_to_plot):
        df = data['rtt_series'].get(nodes)
        if df is not None and not df.empty and 'time' in df.columns:
            plot_exists = True
            jittered_x = np.random.normal(nodes, 0.08, size=len(df)) 
            plt.scatter(jittered_x, df['time'], label=f'{nodes} Nodes ({len(df)} points)', alpha=0.3, s=10, color=colors[i % len(colors)])
        else:
            print(f"Warning: No rtt_series data for {nodes} nodes for overlaid scatter plot.")

    if not plot_exists:
        no_plot_data_msg = "Insufficient data for 4, 6, or 8 nodes in 'rtt_series' to generate overlaid scatter plot."
        print(no_plot_data_msg)
        report_lines.append(no_plot_data_msg)
        return "\n".join(report_lines)

    plt.xlabel("Number of PBFT Nodes (with jitter)")
    plt.ylabel("RTT (ms)")
    plt.title("Overlayed Scatter Plot of All RTT Data Points (rttX.txt for 4, 6, 8 Nodes)")
    plt.xticks(node_configs_to_plot, [str(n) for n in node_configs_to_plot])
    plt.legend()
    plt.grid(True, which="both", ls="--", alpha=0.7)
    plt.tight_layout()

    if not os.path.exists(OUTPUT_DIR): os.makedirs(OUTPUT_DIR)
    plot_path = os.path.join(OUTPUT_DIR, "overlaid_rtt_scatter.png")
    plt.savefig(plot_path)
    print(f"Saved overlaid RTT scatter plot to {plot_path}")
    report_lines.append(f"* *Overlayed Scatter Plot:* Saved as `{os.path.basename(plot_path)}`. "
                        "This plot shows every RTT data point for the 4, 6, and 8 node configurations. It helps to visually assess the density, range, and presence of outliers for each group.")
    return "\n".join(report_lines)


def perform_anova_on_rtt_series(data):
    """
    Performs a one-way ANOVA test on the RTT data from rttX.txt files
    for 4, 6, and 8 node configurations to see if their means are significantly different.
    """
    print("\n\n--- ANOVA Test for RTT Means (4, 6, 8 Nodes from rttX.txt) ---")
    report_lines = ["\n**ANOVA Test for RTT Mean Differences (4, 6, 8 Nodes from rttX.txt)**\n"]
    report_lines.append("A one-way Analysis of Variance (ANOVA) test is performed to determine if there are any statistically significant differences between the mean RTTs of the 4, 6, and 8 PBFT node configurations, using all ping data from the `rttX.txt` files.\n")

    if not data or not data.get('rtt_series'):
        no_data_msg = "No 'rtt_series' data available for ANOVA test."
        print(no_data_msg)
        report_lines.append(no_data_msg)
        return "\n".join(report_lines)

    samples = []
    node_labels = []
    
    for nodes in [4, 6, 8]: 
        df = data['rtt_series'].get(nodes)
        if df is not None and not df.empty and 'time' in df.columns:
            samples.append(df['time'].values) 
            node_labels.append(f"{nodes} Nodes")
        else:
            print(f"Warning: No rtt_series data for {nodes} nodes for ANOVA test. Skipping this group.")
    
    if len(samples) < 2: 
        not_enough_groups_msg = "ANOVA test requires at least two groups with data. Test not performed."
        print(not_enough_groups_msg)
        report_lines.append(not_enough_groups_msg)
        return "\n".join(report_lines)

    f_statistic, p_value = stats.f_oneway(*samples)

    report_lines.append(f"* **Groups Compared:** {', '.join(node_labels)}")
    report_lines.append(f"* **F-statistic:** {f_statistic:.4f}")
    report_lines.append(f"* **P-value:** {p_value:.4g}") 

    alpha = 0.05 
    report_lines.append(f"\n* **Interpretation (at alpha = {alpha}):**")
    if p_value < alpha:
        report_lines.append(f"    * Since the p-value ({p_value:.4g}) is less than the significance level ({alpha}), we reject the null hypothesis. "
                            "This suggests that there is a statistically significant difference in the mean RTT values among at least two of the compared node configurations (4, 6, and 8 nodes).")
        report_lines.append( "    * Further post-hoc tests (e.g., Tukey's HSD) would be needed to determine which specific pairs of groups have significantly different means.")
    else:
        report_lines.append(f"    * Since the p-value ({p_value:.4g}) is greater than or equal to the significance level ({alpha}), we fail to reject the null hypothesis. "
                            "This suggests that there is not enough evidence to conclude a statistically significant difference in the mean RTT values among the 4, 6, and 8 node configurations based on this test.")
    
    print(f"ANOVA Results: F-statistic = {f_statistic:.4f}, p-value = {p_value:.4g}")
    if p_value < alpha:
        print("  Conclusion: Statistically significant difference in mean RTTs found.")
    else:
        print("  Conclusion: No statistically significant difference in mean RTTs found.")
        
    return "\n".join(report_lines)


def analyze_rtt_distribution(data):
    """
    Analyzes and visualizes the distribution of RTT values for each rttX.txt file.
    Provides an in-depth textual summary of the findings.
    """
    print("\n\n--- In-depth RTT Distribution Analysis (from rttX.txt files) ---")
    report_lines = ["\n**RTT Distribution and Variability Analysis (from rttX.txt files)**\n"]
    report_lines.append("This section details the statistical distribution of RTT values obtained from the extended ping tests (100 pings repeated 8 times) for network configurations with 4, 6, and 8 PBFT nodes. This helps understand the consistency and variability of latency.\n")

    if not data or not data.get('rtt_series'):
        no_data_msg = "No 'rtt_series' data available for distribution analysis."
        print(no_data_msg)
        report_lines.append(no_data_msg)
        return "\n".join(report_lines)

    node_counts_for_detailed_dist = [4, 6, 8] # As per provided Markdown

    for nodes in node_counts_for_detailed_dist: 
        df = data['rtt_series'].get(nodes)
        
        if df is None or df.empty or 'time' not in df.columns:
            skip_msg = f"Skipping distribution analysis for {nodes} nodes (rtt{nodes}.txt): No RTT data or 'time' column missing."
            print(f"\n{skip_msg}")
            report_lines.append(f"\n* **{nodes} PBFT Nodes (rtt{nodes}.txt):**\n    * {skip_msg}")
            continue
        
        rtt_values = df['time']
        current_report = [f"\n* **Statistics for {nodes} PBFT Nodes (rtt{nodes}.txt - {len(rtt_values)} total RTT samples):**"]
        current_report.append(f"    * Mean RTT:   {rtt_values.mean():.2f} ms")
        current_report.append(f"    * Median RTT: {rtt_values.median():.2f} ms (50th percentile)")
        current_report.append(f"    * Standard Deviation:  {rtt_values.std():.2f} ms (measure of variability)")
        current_report.append(f"    * Minimum RTT:    {rtt_values.min():.2f} ms")
        current_report.append(f"    * Maximum RTT:    {rtt_values.max():.2f} ms")
        current_report.append(f"    * 25th Percentile (Q1):  {rtt_values.quantile(0.25):.2f} ms")
        current_report.append(f"    * 75th Percentile (Q3):  {rtt_values.quantile(0.75):.2f} ms")
        current_report.append(f"    * Interquartile Range (IQR): {rtt_values.quantile(0.75) - rtt_values.quantile(0.25):.2f} ms")
        
        print("\n".join(current_report).replace("* **", "  ").replace("    * ", "    ")) 
        report_lines.extend(current_report)

        plt.figure(figsize=(10, 6))
        sns.histplot(rtt_values, kde=True, bins=min(50, max(10, len(rtt_values)//10))) 
        plt.title(f"RTT Distribution for {nodes} PBFT Nodes (rtt{nodes}.txt - All Pings)")
        plt.xlabel("RTT (ms)")
        plt.ylabel("Frequency")
        plt.grid(True, ls='--')
        plt.tight_layout()
        if not os.path.exists(OUTPUT_DIR): os.makedirs(OUTPUT_DIR)
        hist_path = os.path.join(OUTPUT_DIR, f"rtt_histogram_allpings_{nodes}nodes.png")
        plt.savefig(hist_path)
        print(f"  Saved RTT histogram (all pings) to {hist_path}")
        report_lines.append(f"    * *Histogram Plot:* Saved as `{os.path.basename(hist_path)}`. This plot shows the shape of the RTT distribution. A unimodal, symmetric distribution is often desirable, while skewness or multiple peaks might indicate inconsistent performance or distinct operational modes.")

        plt.figure(figsize=(8, 7))
        df_copy = df.copy() 
        df_copy['Ping Type'] = df_copy['icmp_seq'].apply(lambda x: 'First Pings (seq=1)' if x == 1 else 'Subsequent Pings (seq>1)')
        sns.boxplot(x='Ping Type', y='time', data=df_copy, palette="pastel")
        plt.title(f"RTT Comparison: First vs. Subsequent Pings\n({nodes} PBFT Nodes - rtt{nodes}.txt)")
        plt.ylabel("RTT (ms)")
        plt.grid(True, axis='y', ls='--')
        plt.tight_layout()
        if not os.path.exists(OUTPUT_DIR): os.makedirs(OUTPUT_DIR)
        box_path = os.path.join(OUTPUT_DIR, f"rtt_boxplot_first_vs_subsequent_{nodes}nodes.png")
        plt.savefig(box_path)
        print(f"  Saved RTT boxplot (first vs. subsequent) to {box_path}")
        report_lines.append(f"    * *Box Plot:* Saved as `{os.path.basename(box_path)}`. This plot compares the RTT distributions of the initial pings (icmp_seq=1) against all subsequent pings within the `rtt{nodes}.txt` dataset. It helps visualize the median, spread (IQR), and potential outliers for these two categories, further illustrating the initial ping overhead.")

    report_lines.append("\n**Summary of RTT Distribution:**\n")
    report_lines.append("The detailed statistics and plots provide a comprehensive view of RTT behavior for 4, 6, and 8 node setups. Key aspects to consider from this analysis are:\n"
                        "- **Central Tendency:** Mean and median RTT give an idea of typical latency.\n"
                        "- **Variability:** Standard deviation and IQR indicate how consistent the RTT is. Higher values mean more unpredictable latency.\n"
                        "- **Outliers:** Min/Max values and the box plots can reveal extreme latency events.\n"
                        "- **Distribution Shape:** Histograms show if latency is normally distributed, skewed, or multimodal.\n"
                        "Comparing these across different node counts (4, 6, 8) can reveal how network size impacts not just average latency but also its consistency.")
    return "\n".join(report_lines)

# --- Main Execution ---
def main():
    """
    Main function to load data and run analyses.
    """
    print("Starting ICMP RTT Analysis...")
    if not os.path.exists(OUTPUT_DIR):
        try:
            os.makedirs(OUTPUT_DIR)
            print(f"Created output directory: {OUTPUT_DIR}")
        except OSError as e:
            print(f"Error creating output directory {OUTPUT_DIR}: {e}")
            return

    all_data = load_data_from_filesystem(DATA_DIRECTORY)

    if not all_data:
        print("Failed to load data. Exiting.")
        return
    
    if not all_data.get('rtt_series') and not all_data.get('first_ping'):
        print(f"No RTT data files (rtt*.txt or *nodes.txt) found in '{DATA_DIRECTORY}'. Please check the DATA_DIRECTORY path and file names. Exiting.")
        return

    arp_results, arp_report = analyze_arp_effect(all_data)
    scaling_report = analyze_rtt_scaling(all_data, arp_results) 
    distribution_report = analyze_rtt_distribution(all_data)
    overlaid_dist_report = plot_overlaid_rtt_distributions(all_data) 
    overlaid_scatter_report = plot_overlaid_scatter_rtt(all_data) 
    anova_report = perform_anova_on_rtt_series(all_data) 
    
    final_report_md = "# In-depth ICMP RTT Analysis Report\n\n"
    final_report_md += "This report provides an in-depth analysis of ICMP Round-Trip Times (RTTs) observed in a PBFT network under various node configurations. The analysis focuses on the impact of ARP, network scaling (vs. N and vs. f), RTT distribution characteristics, and statistical comparisons between node groups.\n"
    final_report_md += arp_report
    final_report_md += "\n\n" + ("-"*80) + "\n\n" 
    final_report_md += scaling_report 
    final_report_md += "\n\n" + ("-"*80) + "\n\n" 
    final_report_md += distribution_report
    final_report_md += "\n\n" + ("-"*80) + "\n\n"
    final_report_md += overlaid_dist_report
    final_report_md += "\n\n" + ("-"*80) + "\n\n"
    final_report_md += overlaid_scatter_report 
    final_report_md += "\n\n" + ("-"*80) + "\n\n"
    final_report_md += anova_report 
    final_report_md += "\n\n---\nEnd of Report."

    report_filename = os.path.join(OUTPUT_DIR, "detailed_rtt_analysis_report.md")
    try:
        with open(report_filename, 'w', encoding='utf-8') as f: # Added encoding
            f.write(final_report_md)
        print(f"\nSuccessfully generated detailed analysis report: {report_filename}")
    except Exception as e:
        print(f"\nError writing detailed analysis report: {e}")

    print(f"\n--- Analysis Complete ---")
    print(f"Plots and Markdown report saved to '{OUTPUT_DIR}' directory.")
    
    # This global variable is used by plt.show() in the if __name__ == "__main__": block.
    # It's a bit of a workaround. A cleaner approach might be to have main return all_data
    # or manage plot showing differently.
    global data 
    data = all_data 

    if data.get('rtt_series') or data.get('first_ping'): 
        plt.show() 
    else:
        print("No data was plotted.")


if __name__ == "__main__":
    # Initialize a global 'data' variable for the script if it's run directly.
    # This helps the plt.show() in main() to function correctly when the script is
    # executed directly, as it relies on this global 'data' being populated.
    data = {} 
    main()