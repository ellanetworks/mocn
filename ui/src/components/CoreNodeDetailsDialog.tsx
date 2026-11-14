import type { ReactNode } from "react";
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Table,
  TableBody,
  TableCell,
  TableRow,
  Tooltip,
} from "@mui/material";
import StateChip from "@/components/StateChip";
import type { AMFState, CoreNodeKind, MMEState } from "@/queries/operators";
import { formatDateTime } from "@/utils/dates";
import { CORE_NODE_KIND_LABELS } from "@/utils/operators";

export interface CoreNodeStatus {
  state: MMEState | AMFState;
  since: string;
  name?: string;
  primaryAddress?: string;
  servedIDs?: string;
  plmnSupport?: string;
  capacity?: number;
  ueConnections: number;
  overload?: string;
}

export interface CoreNodeDetails {
  kind: CoreNodeKind;
  label: string;
  addresses: string[];
  status?: CoreNodeStatus;
}

const NONE = "—";

const SERVED_IDS: Record<CoreNodeKind, { label: string; help: string }> = {
  MME: {
    label: "Served GUMMEIs",
    help: "The identities the MME serves: its PLMNs, MME group IDs and MME codes.",
  },
  AMF: {
    label: "Served GUAMIs",
    help: "The identities the AMF serves, marked unavailable when the AMF says so.",
  },
};

const text = (value: string | undefined) => value ?? NONE;

export default function CoreNodeDetailsDialog({
  node,
  onClose,
}: {
  node: CoreNodeDetails;
  onClose: () => void;
}) {
  const { kind, status } = node;
  const row = (label: string, help: string, value: ReactNode) => ({
    label,
    help,
    value,
  });
  const rows = [
    row(
      "Addresses",
      "The addresses Ella MOCN uses to reach the node (TNL addresses).",
      text(node.addresses.join(", ")),
    ),
    row(
      "Type",
      "MME: the 4G core, reached over S1. AMF: the 5G core, reached over NG.",
      CORE_NODE_KIND_LABELS[kind],
    ),
    row(`${kind} Name`, "The name the node gave itself.", text(status?.name)),
    row(
      "Status",
      "Up: connected and ready. Connected: link up, setup not done yet. Disconnected: no link. Setup failed: the node refused the connection.",
      status ? (
        <StateChip
          key="status"
          state={status.state}
          hint={`Since ${formatDateTime(status.since, { seconds: true })}`}
        />
      ) : (
        NONE
      ),
    ),
    row(
      "Primary Path",
      "The node address Ella MOCN currently sends to (SCTP primary path).",
      text(status?.primaryAddress),
    ),
    row(
      "UE Connections",
      "Devices connected through this node: one per active phone.",
      status ? String(status.ueConnections) : NONE,
    ),
    row(SERVED_IDS[kind].label, SERVED_IDS[kind].help, text(status?.servedIDs)),
    ...(kind === "AMF"
      ? [
          row(
            "PLMN Support List",
            "The PLMNs the AMF supports, each with the slices (S-NSSAIs) it supports.",
            text(status?.plmnSupport),
          ),
        ]
      : []),
    row(
      `Relative ${kind} Capacity`,
      "The node's relative capacity, from 0 to 255. Ella MOCN sends new devices to the operator's nodes in proportion to it, and never to a node with 0.",
      status?.capacity === undefined ? NONE : String(status.capacity),
    ),
    row(
      "Overload",
      "What the node asked to hold back while it is overloaded (Overload Start). None: not overloaded.",
      status ? (status.overload ?? "None") : NONE,
    ),
  ];

  return (
    <Dialog
      open
      onClose={onClose}
      fullWidth
      maxWidth="sm"
      aria-labelledby="core-node-details-title"
    >
      <DialogTitle id="core-node-details-title">{node.label}</DialogTitle>
      <DialogContent dividers>
        <Table size="small" aria-label={`${node.label} details`}>
          <TableBody>
            {rows.map(({ label, help, value }) => (
              <TableRow key={label}>
                <TableCell
                  component="th"
                  scope="row"
                  sx={{ fontWeight: 600, width: "1%", whiteSpace: "nowrap" }}
                >
                  <Tooltip title={help} arrow describeChild>
                    <span>{label}</span>
                  </Tooltip>
                </TableCell>
                <TableCell sx={{ overflowWrap: "anywhere" }}>{value}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Close</Button>
      </DialogActions>
    </Dialog>
  );
}
