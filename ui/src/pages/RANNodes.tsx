import { Box } from "@mui/material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useQuery } from "@tanstack/react-query";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import StateChip from "@/components/StateChip";
import { listRANNodes, type RANNode } from "@/queries/ran_nodes";
import { formatDateTime } from "@/utils/dates";
import { GRID_SX } from "@/utils/layout";
import { formatGlobalENBID, formatPLMN } from "@/utils/operators";

export const RAN_NODES_REFRESH_MS = 5000;

const NONE = "—";

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
    py: 1,
    overflowWrap: "anywhere",
  },
};

interface Row extends RANNode {
  id: number;
}

const columns: GridColDef<Row>[] = [
  {
    field: "global_enb_id",
    headerName: "Global eNB ID",
    description:
      "The identity the RAN node sent in its S1 Setup Request: PLMN, eNB ID type and eNB ID.",
    flex: 1.2,
    minWidth: 220,
    valueGetter: (_value, row) =>
      row.global_enb_id ? formatGlobalENBID(row.global_enb_id) : NONE,
  },
  {
    field: "enb_name",
    headerName: "eNB name",
    description: "The name the RAN node sent in its S1 Setup Request.",
    flex: 0.7,
    minWidth: 120,
    valueGetter: (_value, row) => row.enb_name ?? NONE,
  },
  {
    field: "supported_tas",
    headerName: "Supported TAs",
    description:
      "The tracking areas the RAN node serves, each with the PLMNs it broadcasts.",
    flex: 1,
    minWidth: 180,
    renderCell: ({ row }) =>
      row.supported_tas.length === 0 ? (
        NONE
      ) : (
        <Box
          component="ul"
          aria-label="Supported TAs"
          sx={{ m: 0, p: 0, listStyle: "none" }}
        >
          {row.supported_tas.map(({ tac, broadcast_plmns }) => (
            <li key={tac}>
              {`TAC ${tac}: ${broadcast_plmns.map(formatPLMN).join(", ")}`}
            </li>
          ))}
        </Box>
      ),
  },
  {
    field: "default_paging_drx",
    headerName: "Paging DRX",
    description:
      "The default paging DRX cycle of the RAN node, in radio frames.",
    minWidth: 110,
    valueGetter: (_value, row) =>
      row.default_paging_drx === undefined
        ? NONE
        : String(row.default_paging_drx),
  },
  {
    field: "state",
    headerName: "State",
    description:
      "Connected: SCTP association up, S1 Setup not done yet. Up: S1 Setup succeeded.",
    minWidth: 120,
    renderCell: ({ row }) => <StateChip state={row.state} />,
  },
  {
    field: "connected_since",
    headerName: "Connected since",
    description: "When the RAN node established its SCTP association.",
    flex: 0.7,
    minWidth: 140,
    valueGetter: (_value, row) =>
      formatDateTime(row.connected_since, { seconds: true }),
  },
  {
    field: "tnl_addresses",
    headerName: "TNL addresses",
    description: "The SCTP addresses of the RAN node.",
    flex: 0.8,
    minWidth: 140,
    valueGetter: (_value, row) => row.tnl_addresses.join(", "),
  },
  {
    field: "last_failure_cause",
    headerName: "Last failure",
    description:
      "The cause of the last S1 Setup Failure Ella MOCN sent to the RAN node.",
    flex: 1,
    minWidth: 160,
    valueGetter: (_value, row) => row.last_failure_cause ?? NONE,
  },
];

export default function RANNodes() {
  const nodes = useQuery({
    queryKey: ["ran-nodes"],
    queryFn: listRANNodes,
    refetchInterval: RAN_NODES_REFRESH_MS,
  });

  return (
    <Box component="section" aria-labelledby="ran-nodes-title">
      <PageHeader
        id="ran-nodes-title"
        title="RAN Nodes"
        count={nodes.data?.length}
        description="The eNodeBs connected to Ella MOCN over S1."
      />
      <QueryAlert query={nodes} subject="RAN nodes" />
      <DataGrid<Row>
        aria-label="RAN Nodes"
        rows={(nodes.data ?? []).map((node, id) => ({ ...node, id }))}
        columns={columns}
        loading={nodes.isPending}
        disableColumnSorting
        disableColumnFilter
        disableColumnMenu
        disableRowSelectionOnClick
        disableVirtualization
        hideFooter
        getRowHeight={() => "auto"}
        autoHeight
        localeText={{ noRowsLabel: "No RAN nodes." }}
        sx={gridSx}
      />
    </Box>
  );
}
