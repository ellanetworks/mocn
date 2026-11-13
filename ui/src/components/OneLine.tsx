import type { ReactNode } from "react";
import { Box } from "@mui/material";

export default function OneLine({
  title,
  children,
}: {
  title: string;
  children?: ReactNode;
}) {
  return (
    <Box
      component="span"
      title={title}
      sx={{
        display: "block",
        minWidth: 0,
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
      }}
    >
      {children ?? title}
    </Box>
  );
}
