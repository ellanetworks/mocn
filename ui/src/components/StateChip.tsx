import { Chip, Tooltip, type ChipProps } from "@mui/material";
import type { AMFState, MMEState } from "@/queries/operators";
import type { RadioState } from "@/queries/radios";

type State = MMEState | AMFState | RadioState;

const STATE_LABELS: Record<State, string> = {
  disconnected: "Disconnected",
  connected: "Connected",
  up: "Up",
  setup_failed: "Setup failed",
};

const STATE_COLORS: Record<State, ChipProps["color"]> = {
  disconnected: "default",
  connected: "warning",
  up: "success",
  setup_failed: "default",
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
