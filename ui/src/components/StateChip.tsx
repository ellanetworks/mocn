import { Chip, Tooltip, type ChipProps } from "@mui/material";
import type { AMFState, MMEState, UserPlaneState } from "@/queries/operators";
import type { RadioState } from "@/queries/radios";

type State = MMEState | AMFState | RadioState | UserPlaneState;

const STATE_LABELS: Record<State, string> = {
  disconnected: "Disconnected",
  connected: "Connected",
  up: "Up",
  setup_failed: "Setup failed",
  ready: "Ready",
  unavailable: "Unavailable",
  off: "Off",
};

const STATE_COLORS: Record<State, ChipProps["color"]> = {
  disconnected: "default",
  connected: "warning",
  up: "success",
  setup_failed: "default",
  ready: "success",
  unavailable: "error",
  off: "default",
};

export default function StateChip({
  state,
  hint,
}: {
  state: State;
  hint?: string;
}) {
  const chip = (
    <Chip
      label={STATE_LABELS[state] ?? state}
      color={STATE_COLORS[state] ?? "default"}
      size="small"
    />
  );

  return hint ? (
    <Tooltip title={hint} arrow describeChild>
      {chip}
    </Tooltip>
  ) : (
    chip
  );
}
