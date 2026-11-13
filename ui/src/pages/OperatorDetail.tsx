import { useState } from "react";
import {
  Box,
  Button,
  IconButton,
  Link,
  Skeleton,
  Tooltip,
} from "@mui/material";
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  Edit as EditIcon,
} from "@mui/icons-material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link as RouterLink, useNavigate, useParams } from "react-router-dom";
import ConfirmDialog from "@/components/ConfirmDialog";
import StateChip from "@/components/StateChip";
import {
  CoreNodeDialog,
  DeleteDialog,
  EditPLMNsDialog,
  EditGatewayIdentityDialog,
  EditPWSOperatorDialog,
  RenameOperatorDialog,
} from "@/components/OperatorDialogs";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import SectionHeader from "@/components/SectionHeader";
import SettingsTable from "@/components/SettingsTable";
import { useAuth } from "@/contexts/AuthContext";
import { errorMessage, useSnackbar } from "@/contexts/SnackbarContext";
import {
  deleteOperator,
  getOperator,
  type CoreNode,
  type CoreNodeKind,
  type MME,
  type MMEStatus,
  type Operator,
  type RadioUEConnections,
} from "@/queries/operators";
import { radioPath } from "@/queries/radios";
import { formatDateTime } from "@/utils/dates";
import { GRID_SX } from "@/utils/layout";
import {
  formatGlobalENBID,
  formatGlobalGNBID,
  formatGUMMEI,
  formatOverload,
  formatOverloadDetail,
  formatPLMN,
  formatTNLAddresses,
  GATEWAY_IDENTITY_HELP,
  operatorPath,
  PWS_HELP,
} from "@/utils/operators";

export const OPERATOR_REFRESH_MS = 5000;

const operatorQueryKey = (name: string) => ["operators", "operator", name];

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
  },
};

type Editing =
  | { type: "name" }
  | { type: "plmns" }
  | { type: "pws" }
  | { type: "identity" }
  | { type: "add" }
  | { type: "edit"; kind: CoreNodeKind; index: number }
  | { type: "delete"; kind: CoreNodeKind; index: number };

interface NodeRow {
  id: string;
  kind: CoreNodeKind;
  index: number;
  addresses: string[];
  status?: MMEStatus;
}

const NONE = "—";

const KIND_RAT: Record<CoreNodeKind, string> = { MME: "4G", AMF: "5G" };

const NODE_COLUMNS: GridColDef<NodeRow>[] = [
  {
    field: "addresses",
    headerName: "Addresses",
    description: "The TNL addresses Ella MOCN uses to reach the node.",
    flex: 0.8,
    minWidth: 150,
    renderCell: ({ row }) => <OneLine title={row.addresses.join(", ")} />,
  },
  {
    field: "kind",
    headerName: "Type",
    description: "MME: the 4G core, over S1. AMF: the 5G core, over NG.",
    flex: 0.3,
    minWidth: 100,
    valueGetter: (_value, row) => `${row.kind} (${KIND_RAT[row.kind]})`,
  },
  {
    field: "mme_name",
    headerName: "Name",
    description: "The name the MME sent in its S1 Setup Response.",
    flex: 0.6,
    minWidth: 120,
    valueGetter: (_value, row) => row.status?.mme_name ?? NONE,
  },
  {
    field: "served_ids",
    headerName: "Served IDs",
    description:
      "The GUMMEIs the MME serves, from its S1 Setup Response: PLMNs, MME group IDs and MME codes.",
    flex: 1,
    minWidth: 200,
    renderCell: ({ row }) =>
      row.status?.served_gummeis?.length ? (
        <OneLine
          title={row.status.served_gummeis.map(formatGUMMEI).join("; ")}
        />
      ) : (
        NONE
      ),
  },
  {
    field: "ue_connections",
    headerName: "UE connections",
    description:
      "The UE-associated logical S1-connections Ella MOCN relays to the MME, one per UE in ECM-CONNECTED.",
    flex: 0.4,
    minWidth: 140,
    valueGetter: (_value, row) =>
      row.status ? String(row.status.ue_connections) : NONE,
  },
  {
    field: "overload",
    headerName: "Overload",
    description:
      "The overload action the MME requested in OVERLOAD START, which Ella MOCN relays to the radios that broadcast one of the operator's PLMNs, with the GUMMEIs it applies to. None: no ongoing overload action.",
    flex: 0.6,
    minWidth: 140,
    renderCell: ({ row }) => {
      if (!row.status) {
        return NONE;
      }
      const overload = row.status.overload ?? [];
      return overload.length ? (
        <OneLine title={formatOverloadDetail(overload)}>
          {formatOverload(overload)}
        </OneLine>
      ) : (
        "None"
      );
    },
  },
  {
    field: "state",
    headerName: "Status",
    description:
      "Disconnected: no SCTP association to the MME. Connected: SCTP association up, S1 Setup not done yet. Up: S1 Setup succeeded. Setup failed: the MME rejected the S1 Setup.",
    flex: 0.4,
    minWidth: 150,
    renderCell: ({ row }) =>
      row.status ? (
        <StateChip
          state={row.status.state}
          hint={`Since ${formatDateTime(row.status.since, { seconds: true })}`}
        />
      ) : (
        NONE
      ),
  },
];

const RADIO_COLUMNS: GridColDef<RadioUEConnections>[] = [
  {
    field: "radio",
    headerName: "Radio",
    description:
      "A radio that broadcasts one of the operator's PLMNs, or that has UE connections for it.",
    flex: 1,
    minWidth: 160,
    renderCell: ({ row }) => (
      <Link
        component={RouterLink}
        to={radioPath(row.radio)}
        variant="body2"
        sx={{ color: "link", textDecoration: "underline" }}
      >
        {row.name ?? row.radio}
      </Link>
    ),
  },
  {
    field: "ue_connections",
    headerName: "UE connections",
    description:
      "The UE-associated logical S1-connections Ella MOCN relays from the radio to the operator, one per UE in ECM-CONNECTED.",
    flex: 0.5,
    minWidth: 140,
  },
];

function CoreNodes({
  mmes,
  amfs,
  identity,
  editable,
  onAdd,
  onEdit,
  onDelete,
}: {
  mmes: MME[];
  amfs: CoreNode[];
  identity: Record<CoreNodeKind, boolean>;
  editable: boolean;
  onAdd: () => void;
  onEdit: (kind: CoreNodeKind, index: number) => void;
  onDelete: (kind: CoreNodeKind, index: number) => void;
}) {
  const rows: NodeRow[] = [
    ...mmes.map((node, index) => ({
      id: `MME ${node.tnl_addresses.join(",")}`,
      kind: "MME" as const,
      index,
      addresses: node.tnl_addresses,
      status: node.status,
    })),
    ...amfs.map((node, index) => ({
      id: `AMF ${node.tnl_addresses.join(",")}`,
      kind: "AMF" as const,
      index,
      addresses: node.tnl_addresses,
    })),
  ];
  const columns: GridColDef<NodeRow>[] = [...NODE_COLUMNS];
  if (editable) {
    columns.push({
      field: "actions",
      headerName: "Actions",
      width: 100,
      align: "right",
      headerAlign: "right",
      renderCell: ({ row }) => {
        const name = row.addresses.join(", ");
        return (
          <>
            <Tooltip title={`Edit ${row.kind}`} arrow>
              <IconButton
                size="small"
                aria-label={`Edit ${row.kind} ${name}`}
                onClick={() => onEdit(row.kind, row.index)}
              >
                <EditIcon fontSize="small" color="primary" />
              </IconButton>
            </Tooltip>
            <Tooltip title={`Delete ${row.kind}`} arrow>
              <IconButton
                size="small"
                aria-label={`Delete ${row.kind} ${name}`}
                onClick={() => onDelete(row.kind, row.index)}
              >
                <DeleteIcon fontSize="small" color="primary" />
              </IconButton>
            </Tooltip>
          </>
        );
      },
    });
  }
  const addable = identity.MME || identity.AMF;

  return (
    <>
      <SectionHeader
        id="core-nodes-title"
        title={`Core network nodes (${rows.length})`}
        description="The MMEs and AMFs of this operator's core, each with its TNL addresses."
      >
        {editable && (
          <Tooltip
            title={addable ? "" : "Set the gateway identity first"}
            describeChild
          >
            <span>
              <Button
                variant="contained"
                color="success"
                startIcon={<AddIcon />}
                onClick={onAdd}
                disabled={!addable}
                sx={{ flexShrink: 0 }}
              >
                Add
              </Button>
            </span>
          </Tooltip>
        )}
      </SectionHeader>
      <DataGrid<NodeRow>
        aria-labelledby="core-nodes-title"
        rows={rows}
        columns={columns}
        disableColumnSorting
        disableColumnFilter
        disableColumnMenu
        disableRowSelectionOnClick
        disableVirtualization
        hideFooter
        localeText={{ noRowsLabel: "No core network nodes." }}
        autoHeight
        sx={gridSx}
      />
    </>
  );
}

function GatewayIdentity({ parts }: { parts: [string, string | undefined][] }) {
  return (
    <Box>
      {parts.map(([rat, id]) => (
        <OneLine key={rat} title={`${rat} ${id ?? "Not set"}`}>
          <Box component="span" sx={{ fontWeight: 600, mr: 1 }}>
            {rat}
          </Box>
          {id ?? (
            <Box component="span" sx={{ color: "text.secondary" }}>
              Not set
            </Box>
          )}
        </OneLine>
      ))}
    </Box>
  );
}

function OperatorSections({
  operator,
  editable,
  onRenamed,
}: {
  operator: Operator;
  editable: boolean;
  onRenamed: (name: string) => void;
}) {
  const [editing, setEditing] = useState<Editing | null>(null);
  const close = () => setEditing(null);
  const edit = (next: Editing) =>
    editable ? () => setEditing(next) : undefined;

  const enb = operator.global_enb_id ?? undefined;
  const gnb = operator.global_gnb_id ?? undefined;
  const mmes = operator.mmes ?? [];
  const amfs = operator.amfs ?? [];
  const deleting =
    editing?.type === "delete"
      ? (editing.kind === "MME" ? mmes : amfs)[editing.index]
      : undefined;

  return (
    <>
      <SectionHeader
        id="overview-title"
        title="Overview"
        description="How Ella MOCN identifies this operator."
        first
      />
      <SettingsTable
        label="Overview"
        rows={[
          {
            label: "Name",
            help: "The name of this operator in Ella MOCN.",
            value: operator.name,
            onEdit: edit({ type: "name" }),
          },
          {
            label: "PLMNs",
            help: "The PLMN IDs of this operator. UEs that select one of them are routed to this operator's core.",
            value: (
              <OneLine title={operator.plmns.map(formatPLMN).join(", ")} />
            ),
            onEdit: edit({ type: "plmns" }),
          },
          {
            label: "PWS operator",
            help: PWS_HELP,
            value: operator.pws_operator ? "Yes" : "No",
            onEdit: edit({ type: "pws" }),
          },
          {
            label: "Gateway identity",
            help: GATEWAY_IDENTITY_HELP,
            dense: true,
            value: (
              <GatewayIdentity
                parts={[
                  ["4G", enb ? formatGlobalENBID(enb) : undefined],
                  ["5G", gnb ? formatGlobalGNBID(gnb) : undefined],
                ]}
              />
            ),
            onEdit: edit({ type: "identity" }),
          },
        ]}
      />

      <CoreNodes
        mmes={mmes}
        amfs={amfs}
        identity={{ MME: enb !== undefined, AMF: gnb !== undefined }}
        editable={editable}
        onAdd={() => setEditing({ type: "add" })}
        onEdit={(kind, index) => setEditing({ type: "edit", kind, index })}
        onDelete={(kind, index) => setEditing({ type: "delete", kind, index })}
      />

      <SectionHeader
        id="ue-connections-title"
        title="UE connections by radio"
        description="The UE connections Ella MOCN relays to this operator from each radio."
      />
      <DataGrid<RadioUEConnections>
        aria-labelledby="ue-connections-title"
        rows={operator.ue_connections_by_radio ?? []}
        columns={RADIO_COLUMNS}
        getRowId={(row) => row.radio}
        disableColumnSorting
        disableColumnFilter
        disableColumnMenu
        disableRowSelectionOnClick
        disableVirtualization
        hideFooter
        autoHeight
        localeText={{ noRowsLabel: "No radios." }}
        sx={gridSx}
      />

      {editing?.type === "name" && (
        <RenameOperatorDialog
          operator={operator}
          onClose={close}
          onRenamed={onRenamed}
        />
      )}
      {editing?.type === "plmns" && (
        <EditPLMNsDialog operator={operator} onClose={close} />
      )}
      {editing?.type === "pws" && (
        <EditPWSOperatorDialog operator={operator} onClose={close} />
      )}
      {editing?.type === "identity" && (
        <EditGatewayIdentityDialog operator={operator} onClose={close} />
      )}
      {editing?.type === "add" && (
        <CoreNodeDialog operator={operator} onClose={close} />
      )}
      {editing?.type === "edit" && (
        <CoreNodeDialog
          operator={operator}
          kind={editing.kind}
          index={editing.index}
          onClose={close}
        />
      )}
      {editing?.type === "delete" && deleting && (
        <DeleteDialog
          operator={operator}
          title={`Delete ${editing.kind}`}
          description={`Delete ${editing.kind} ${formatTNLAddresses(deleting)}?`}
          change={
            editing.kind === "MME"
              ? { mmes: mmes.filter((_, i) => i !== editing.index) }
              : { amfs: amfs.filter((_, i) => i !== editing.index) }
          }
          message={`${editing.kind} deleted successfully.`}
          onClose={close}
        />
      )}
    </>
  );
}

export default function OperatorDetail() {
  const name = useParams<{ name: string }>().name ?? "";
  const auth = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { showSnackbar } = useSnackbar();
  const [deleting, setDeleting] = useState(false);

  const operator = useQuery({
    queryKey: operatorQueryKey(name),
    queryFn: () => getOperator(name),
    refetchInterval: OPERATOR_REFRESH_MS,
  });

  const remove = useMutation({
    mutationFn: () => deleteOperator(name),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: operatorQueryKey(name) });
      void queryClient.invalidateQueries({ queryKey: ["operators"] });
      showSnackbar("Operator deleted successfully.", "success");
      navigate("/operators", { replace: true });
    },
    onError: (error) => {
      setDeleting(false);
      showSnackbar(
        `Failed to delete operator: ${errorMessage(error)}`,
        "error",
      );
    },
  });

  const editable = auth.can("operator:update") && operator.data !== undefined;

  return (
    <Box component="section" aria-labelledby="operator-title">
      <PageHeader
        id="operator-title"
        title={name}
        parent={{ label: "Operators", to: "/operators" }}
        action={
          editable && (
            <Button
              variant="outlined"
              color="error"
              onClick={() => setDeleting(true)}
            >
              Delete
            </Button>
          )
        }
      />
      <QueryAlert query={operator} subject="this operator" />
      {operator.data ? (
        <OperatorSections
          operator={operator.data}
          editable={editable}
          onRenamed={(renamed) => {
            queryClient.removeQueries({ queryKey: operatorQueryKey(name) });
            navigate(operatorPath(renamed), { replace: true });
          }}
        />
      ) : (
        operator.isPending && <Skeleton variant="rounded" height={240} />
      )}
      {deleting && (
        <ConfirmDialog
          title="Delete Operator"
          description={`Delete ${name}? This cannot be undone.`}
          confirmLabel="Delete"
          pending={remove.isPending}
          onConfirm={() => remove.mutate()}
          onClose={() => setDeleting(false)}
        />
      )}
    </Box>
  );
}
