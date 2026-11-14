import type { ReactNode } from "react";
import {
  Box,
  IconButton,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableRow,
  Tooltip,
} from "@mui/material";
import EditIcon from "@mui/icons-material/Edit";
import { TABLE_CONTAINER_SX } from "@/utils/layout";

const ELLIPSIS_SX = {
  display: "block",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
} as const;

export interface SettingRow {
  label: string;
  help: string;
  value: ReactNode;
  dense?: boolean;
  onEdit?: () => void;
}

export default function SettingsTable({
  rows,
  label,
}: {
  rows: SettingRow[];
  label: string;
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
                {row.value}
              </TableCell>
              <TableCell sx={{ width: "10%", textAlign: "right" }}>
                {row.onEdit && (
                  <IconButton
                    size="small"
                    onClick={row.onEdit}
                    aria-label={`Edit ${row.label}`}
                  >
                    <EditIcon fontSize="small" color="primary" />
                  </IconButton>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </TableContainer>
  );
}
