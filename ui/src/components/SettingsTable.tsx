import type { ReactNode } from "react";
import {
  Box,
  IconButton,
  Skeleton,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableRow,
  Tooltip,
} from "@mui/material";
import EditIcon from "@mui/icons-material/Edit";
import CopyButton from "@/components/CopyButton";
import { TABLE_CONTAINER_SX } from "@/utils/layout";

const ELLIPSIS_SX = {
  display: "block",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
} as const;

export interface Copy {
  value: string;
  label: string;
}

export const LINE_SX = {
  minHeight: 30,
  display: "flex",
  alignItems: "center",
  gap: 1,
};

export interface SettingRow {
  label: string;
  help: string;
  value: ReactNode;
  dense?: boolean;
  onEdit?: () => void;
  copy?: Copy | Copy[];
}

export default function SettingsTable({
  rows,
  label,
  loading = false,
}: {
  rows: SettingRow[];
  label: string;
  loading?: boolean;
}) {
  return (
    <TableContainer sx={TABLE_CONTAINER_SX}>
      <Table aria-label={label}>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.label} sx={{ height: 63 }}>
              <TableCell sx={{ fontWeight: 600, width: "35%", maxWidth: 0 }}>
                <Tooltip title={row.help} arrow describeChild>
                  <Box component="span" sx={ELLIPSIS_SX}>
                    {row.label}
                  </Box>
                </Tooltip>
              </TableCell>
              <TableCell
                sx={{
                  width: "55%",
                  maxWidth: 0,
                  overflowWrap: "anywhere",
                  ...(row.dense && { py: 0 }),
                }}
              >
                {loading ? <Skeleton variant="text" width={160} /> : row.value}
              </TableCell>
              <TableCell sx={{ width: "10%" }}>
                <Stack
                  direction="row"
                  sx={{ justifyContent: "flex-end", gap: 0.5 }}
                >
                  {row.copy && !loading && (
                    <Stack sx={{ gap: 0.5 }}>
                      {[row.copy].flat().map((c) => (
                        <Box key={c.value} sx={LINE_SX}>
                          <CopyButton value={c.value} label={c.label} />
                        </Box>
                      ))}
                    </Stack>
                  )}
                  {row.onEdit && (
                    <IconButton
                      size="small"
                      onClick={row.onEdit}
                      disabled={loading}
                      aria-label={`Edit ${row.label}`}
                    >
                      <EditIcon fontSize="small" color="primary" />
                    </IconButton>
                  )}
                </Stack>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </TableContainer>
  );
}
