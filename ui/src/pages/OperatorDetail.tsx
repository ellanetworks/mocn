import { useState } from "react";
import { Box, Button, IconButton, Skeleton, Tooltip } from "@mui/material";
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  Edit as EditIcon,
} from "@mui/icons-material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
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
  type MME,
  type MMEStatus,
  type Operator,
} from "@/queries/operators";
import { formatDateTime } from "@/utils/dates";
import { GRID_SX } from "@/utils/layout";
import {
  formatGlobalENBID,
  formatGlobalGNBID,
  formatGUMMEI,
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

type Kind = "MME" | "AMF";

type Editing =
  | { type: "name" }
  | { type: "plmns" }
  | { type: "pws" }
  | { type: "identity" }
  | { type: "add"; kind: Kind }
  | { type: "edit"; kind: Kind; index: number }
  | { type: "delete"; kind: Kind; index: number };

interface NodeRow {
  index: number;
  addresses: string[];
  status?: MMEStatus;
}

const NONE = "—";

const MME_COLUMNS: GridColDef<NodeRow>[] = [
  {
    field: "mme_name",
    headerName: "Name",
    description: "The name the MME sent in its S1 Setup Response.",
    flex: 0.6,
    minWidth: 120,
    valueGetter: (_value, row) => row.status?.mme_name ?? NONE,
  },
  {
    field: "gummeis",
    headerName: "Served GUMMEIs",
    description:
      "The PLMNs, MME group IDs and MME codes that the MME serves, from its S1 Setup Response.",
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
    field: "state",
    headerName: "Status",
    description:
      "Disconnected: no SCTP association to the MME. Connected: SCTP association up, S1 Setup not done yet. Up: S1 Setup succeeded. Setup failed: the MME rejected the S1 Setup.",
    flex: 0.4,
    minWidth: 120,
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

const KIND_RAT: Record<Kind, string> = { MME: "4G", AMF: "5G" };

function CoreNodes({
  kind,
  nodes,
  identitySet,
  editable,
  onAdd,
  onEdit,
  onDelete,
}: {
  kind: Kind;
  nodes: MME[];
  identitySet: boolean;
  editable: boolean;
  onAdd: () => void;
  onEdit: (index: number) => void;
  onDelete: (index: number) => void;
}) {
  const id = `${kind.toLowerCase()}s-title`;
  const columns: GridColDef<NodeRow>[] = [
    {
      field: "addresses",
      headerName: "Addresses",
      description: `The TNL addresses Ella MOCN uses to reach the ${kind}.`,
      flex: 0.8,
      minWidth: 150,
      renderCell: ({ row }) => <OneLine title={row.addresses.join(", ")} />,
    },
    ...(kind === "MME" ? MME_COLUMNS : []),
  ];
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
            <Tooltip title={`Edit ${kind}`} arrow>
              <IconButton
                size="small"
                aria-label={`Edit ${kind} ${name}`}
                onClick={() => onEdit(row.index)}
              >
                <EditIcon fontSize="small" color="primary" />
              </IconButton>
            </Tooltip>
            <Tooltip title={`Delete ${kind}`} arrow>
              <IconButton
                size="small"
                aria-label={`Delete ${kind} ${name}`}
                onClick={() => onDelete(row.index)}
              >
                <DeleteIcon fontSize="small" color="primary" />
              </IconButton>
            </Tooltip>
          </>
        );
      },
    });
  }

  return (
    <>
      <SectionHeader
        id={id}
        title={`${KIND_RAT[kind]} ${kind}s (${nodes.length})`}
        description={`The ${kind}s of this operator's ${KIND_RAT[kind]} core, each with its TNL addresses.`}
      >
        {editable && (
          <Tooltip
            title={identitySet ? "" : "Set the gateway identity first"}
            describeChild
          >
            <span>
              <Button
                variant="contained"
                color="success"
                startIcon={<AddIcon />}
                onClick={onAdd}
                disabled={!identitySet}
                sx={{ flexShrink: 0 }}
              >
                Add {kind}
              </Button>
            </span>
          </Tooltip>
        )}
      </SectionHeader>
      <DataGrid<NodeRow>
        aria-labelledby={id}
        rows={nodes.map((node, index) => ({
          index,
          addresses: node.tnl_addresses,
          status: node.status,
        }))}
        columns={columns}
        getRowId={(row) => row.addresses.join(",")}
        disableColumnSorting
        disableColumnFilter
        disableColumnMenu
        disableRowSelectionOnClick
        disableVirtualization
        hideFooter
        localeText={{ noRowsLabel: `No ${kind}s.` }}
        autoHeight
        sx={gridSx}
      />
    </>
  );
}

function GatewayIdentity({ parts }: { parts: [string, string | undefined][] }) {
  return (
    <OneLine
      title={parts.map(([rat, id]) => `${rat} ${id ?? "Not set"}`).join("  ")}
    >
      {parts.map(([rat, id], i) => (
        <Box component="span" key={rat} sx={{ ml: i > 0 ? 4 : 0 }}>
          <Box component="span" sx={{ fontWeight: 600, mr: 1 }}>
            {rat}
          </Box>
          {id ?? (
            <Box component="span" sx={{ color: "text.secondary" }}>
              Not set
            </Box>
          )}
        </Box>
      ))}
    </OneLine>
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
        kind="MME"
        nodes={mmes}
        identitySet={enb !== undefined}
        editable={editable}
        onAdd={() => setEditing({ type: "add", kind: "MME" })}
        onEdit={(index) => setEditing({ type: "edit", kind: "MME", index })}
        onDelete={(index) => setEditing({ type: "delete", kind: "MME", index })}
      />

      <CoreNodes
        kind="AMF"
        nodes={amfs}
        identitySet={gnb !== undefined}
        editable={editable}
        onAdd={() => setEditing({ type: "add", kind: "AMF" })}
        onEdit={(index) => setEditing({ type: "edit", kind: "AMF", index })}
        onDelete={(index) => setEditing({ type: "delete", kind: "AMF", index })}
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
        <CoreNodeDialog
          operator={operator}
          kind={editing.kind}
          onClose={close}
        />
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
