import { useState } from "react";
import {
  Box,
  Button,
  IconButton,
  Skeleton,
  Tooltip,
  Typography,
} from "@mui/material";
import { Delete as DeleteIcon } from "@mui/icons-material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import ConfirmDialog from "@/components/ConfirmDialog";
import {
  AddCoreNodeDialog,
  EditPLMNsDialog,
  EditGatewayIdentityDialog,
  EditPWSOperatorDialog,
  RemoveDialog,
  RenameOperatorDialog,
} from "@/components/OperatorDialogs";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import SectionHeader from "@/components/SectionHeader";
import SettingsTable, { SubFields } from "@/components/SettingsTable";
import { useAuth } from "@/contexts/AuthContext";
import { errorMessage, useSnackbar } from "@/contexts/SnackbarContext";
import {
  type CoreNode,
  deleteOperator,
  getOperator,
  type Operator,
} from "@/queries/operators";
import { GRID_SX } from "@/utils/layout";
import {
  ENB_IDENTITY_LABEL,
  formatGlobalENBID,
  formatGlobalGNBID,
  formatPLMN,
  formatTNLAddresses,
  GATEWAY_IDENTITY_HELP,
  GNB_IDENTITY_LABEL,
  operatorPath,
  PWS_HELP,
} from "@/utils/operators";

const operatorQueryKey = (name: string) => ["operators", "operator", name];

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
    overflowWrap: "anywhere",
  },
};

type Kind = "MME" | "AMF";

type Editing =
  | { type: "name" }
  | { type: "plmns" }
  | { type: "pws" }
  | { type: "identity" }
  | { type: "add"; kind: Kind }
  | { type: "remove"; kind: Kind; index: number };

interface NodeRow {
  index: number;
  addresses: string;
}

function NotSet() {
  return (
    <Typography variant="body2" color="textSecondary">
      Not set
    </Typography>
  );
}

function PLMNList({ plmns }: { plmns: string[] }) {
  return (
    <Box
      component="ul"
      aria-label="PLMNs"
      sx={{ m: 0, p: 0, listStyle: "none" }}
    >
      {plmns.map((plmn) => (
        <li key={plmn}>{plmn}</li>
      ))}
    </Box>
  );
}

const KIND_RAT: Record<Kind, string> = { MME: "4G", AMF: "5G" };

function CoreNodes({
  kind,
  nodes,
  identitySet,
  editable,
  onAdd,
  onRemove,
}: {
  kind: Kind;
  nodes: CoreNode[];
  identitySet: boolean;
  editable: boolean;
  onAdd: () => void;
  onRemove: (index: number) => void;
}) {
  const id = `${kind.toLowerCase()}s-title`;
  const columns: GridColDef<NodeRow>[] = [
    {
      field: "addresses",
      headerName: "TNL addresses",
      flex: 1,
      minWidth: 180,
    },
  ];
  if (editable) {
    columns.push({
      field: "actions",
      headerName: "",
      width: 64,
      align: "right",
      sortable: false,
      renderCell: ({ row }) => (
        <IconButton
          size="small"
          aria-label={`Remove ${kind} ${row.addresses}`}
          onClick={() => onRemove(row.index)}
        >
          <DeleteIcon fontSize="small" color="primary" />
        </IconButton>
      ),
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
                size="small"
                onClick={onAdd}
                disabled={!identitySet}
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
          addresses: formatTNLAddresses(node),
        }))}
        columns={columns}
        getRowId={(row) => row.addresses}
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
  const removing =
    editing?.type === "remove"
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
            value: <PLMNList plmns={operator.plmns.map(formatPLMN)} />,
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
              <SubFields
                rows={[
                  [
                    ENB_IDENTITY_LABEL,
                    enb ? formatGlobalENBID(enb) : <NotSet key="enb" />,
                  ],
                  [
                    GNB_IDENTITY_LABEL,
                    gnb ? formatGlobalGNBID(gnb) : <NotSet key="gnb" />,
                  ],
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
        onRemove={(index) => setEditing({ type: "remove", kind: "MME", index })}
      />

      <CoreNodes
        kind="AMF"
        nodes={amfs}
        identitySet={gnb !== undefined}
        editable={editable}
        onAdd={() => setEditing({ type: "add", kind: "AMF" })}
        onRemove={(index) => setEditing({ type: "remove", kind: "AMF", index })}
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
        <AddCoreNodeDialog
          operator={operator}
          kind={editing.kind}
          onClose={close}
        />
      )}
      {editing?.type === "remove" && removing && (
        <RemoveDialog
          operator={operator}
          title={`Remove ${editing.kind}`}
          description={`Remove ${editing.kind} ${formatTNLAddresses(removing)}?`}
          change={
            editing.kind === "MME"
              ? { mmes: mmes.filter((_, i) => i !== editing.index) }
              : { amfs: amfs.filter((_, i) => i !== editing.index) }
          }
          message={`${editing.kind} removed successfully.`}
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
