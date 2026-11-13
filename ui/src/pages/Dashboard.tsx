import type { ReactNode } from "react";
import {
  Box,
  Card,
  type CardProps,
  CardActionArea,
  CardContent,
  CardHeader,
  Skeleton,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tooltip,
  Typography,
} from "@mui/material";
import Grid from "@mui/material/Grid";
import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink, useNavigate } from "react-router-dom";
import PageHeader from "@/components/PageHeader";
import { getMetrics, type Sample, sumOf } from "@/queries/metrics";
import { listOperators } from "@/queries/operators";
import { formatDateTime } from "@/utils/dates";
import { operatorPath } from "@/utils/operators";

export const DASHBOARD_REFRESH_MS = 5000;

const nf = new Intl.NumberFormat();

const BYTE_UNITS = ["B", "KiB", "MiB", "GiB", "TiB"];

const sizeFormat = new Intl.NumberFormat(undefined, {
  maximumFractionDigits: 2,
});

export const formatBytes = (bytes: number): string => {
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < BYTE_UNITS.length - 1) {
    n /= 1024;
    i++;
  }
  return `${sizeFormat.format(n)} ${BYTE_UNITS[i]}`;
};

const headerSx = {
  backgroundColor: "backgroundSubtle",
  borderTopLeftRadius: 8,
  borderTopRightRadius: 8,
  "& .MuiCardHeader-title": { fontWeight: 600, fontSize: "1rem" },
  "& .MuiCardHeader-action": { alignSelf: "center", m: 0 },
};

const titleLinkSx = {
  textDecoration: "none",
  color: "inherit",
  "&:hover": { textDecoration: "underline" },
};

const headSx = { fontWeight: 600, whiteSpace: "nowrap" };

interface PanelProps extends Omit<CardProps, "title"> {
  title: string;
  to?: string;
  loading?: boolean;
  error?: boolean;
  minHeight?: number;
  alignTop?: boolean;
  children?: ReactNode;
}

function Panel({
  title,
  to,
  loading,
  error,
  minHeight = 160,
  alignTop,
  children,
  ...cardProps
}: PanelProps) {
  return (
    <Card
      {...cardProps}
      sx={{
        height: "100%",
        display: "flex",
        flexDirection: "column",
        borderRadius: 3,
        boxShadow: 2,
      }}
    >
      <CardHeader
        title={
          to ? (
            <Box component={RouterLink} to={to} sx={titleLinkSx}>
              {title}
            </Box>
          ) : (
            title
          )
        }
        slotProps={{ title: { component: "h3" } }}
        sx={headerSx}
      />
      <CardContent
        sx={{
          flexGrow: 1,
          display: "flex",
          alignItems: alignTop ? "flex-start" : "center",
          justifyContent: alignTop ? "flex-start" : "center",
          minHeight,
        }}
      >
        {loading ? (
          <Skeleton
            variant="rounded"
            width={alignTop ? "100%" : 120}
            height={alignTop ? minHeight - 32 : 40}
          />
        ) : error ? (
          <Typography variant="body2" color="error">
            Failed to load data.
          </Typography>
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}

interface KpiCardProps {
  title: string;
  hint: string;
  value?: string;
  loading?: boolean;
  error?: boolean;
  to?: string;
}

function KpiCard({ title, hint, value, to, ...state }: KpiCardProps) {
  const content = <Typography variant="h4">{value}</Typography>;

  if (to) {
    return (
      <Tooltip title={hint} arrow describeChild>
        <CardActionArea
          component={RouterLink}
          to={to}
          sx={{ height: "100%", borderRadius: 3 }}
        >
          <Panel title={title} {...state}>
            {content}
          </Panel>
        </CardActionArea>
      </Tooltip>
    );
  }

  return (
    <Tooltip title={hint} arrow describeChild>
      <Panel title={title} tabIndex={0} {...state}>
        {content}
      </Panel>
    </Tooltip>
  );
}

function Section({
  title,
  first,
  children,
}: {
  title: string;
  first?: boolean;
  children: ReactNode;
}) {
  return (
    <Box component="section" aria-label={title}>
      <Typography variant="h5" component="h2" sx={{ mt: first ? 0 : 4, mb: 2 }}>
        {title}
      </Typography>
      <Grid container spacing={4} sx={{ alignItems: "stretch" }}>
        {children}
      </Grid>
    </Box>
  );
}

const THIRD = { xs: 12, md: 4 };

const failedToLoad = (q: { error: Error | null; data: unknown }) =>
  !!q.error && q.data === undefined;

interface OperatorFigures {
  name: string;
  up: number;
  total: number;
  ueConnections: number;
}

const operatorFigures = (samples: Sample[]): OperatorFigures[] => {
  const names = new Set(
    samples
      .filter(
        (s) =>
          (s.name === "ellamocn_core_associations" ||
            s.name === "ellamocn_ue_connections") &&
          s.labels.operator !== undefined,
      )
      .map((s) => s.labels.operator),
  );
  return [...names]
    .sort((a, b) => a.localeCompare(b))
    .map((name) => ({
      name,
      up:
        sumOf(samples, "ellamocn_core_associations", {
          operator: name,
          state: "up",
        }) ?? 0,
      total:
        sumOf(samples, "ellamocn_core_associations", { operator: name }) ?? 0,
      ueConnections:
        sumOf(samples, "ellamocn_ue_connections", { operator: name }) ?? 0,
    }));
};

function OperatorsTable({ operators }: { operators: OperatorFigures[] }) {
  const navigate = useNavigate();

  if (operators.length === 0) {
    return (
      <Typography variant="body2" color="textSecondary">
        No operators.
      </Typography>
    );
  }

  const total = operators.reduce(
    (sum, o) => ({
      up: sum.up + o.up,
      total: sum.total + o.total,
      ueConnections: sum.ueConnections + o.ueConnections,
    }),
    { up: 0, total: 0, ueConnections: 0 },
  );

  return (
    <TableContainer>
      <Table size="small" aria-label="Operators">
        <TableHead>
          <TableRow>
            <TableCell sx={headSx}>Operator</TableCell>
            <TableCell sx={headSx} align="right">
              Core nodes up
            </TableCell>
            <TableCell sx={headSx} align="right">
              UE connections
            </TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {operators.map((o) => (
            <TableRow
              key={o.name}
              hover
              onClick={() => navigate(operatorPath(o.name))}
              sx={{ cursor: "pointer" }}
            >
              <TableCell>
                <Box
                  component={RouterLink}
                  to={operatorPath(o.name)}
                  onClick={(e) => e.stopPropagation()}
                >
                  {o.name}
                </Box>
              </TableCell>
              <TableCell align="right">
                {`${nf.format(o.up)} / ${nf.format(o.total)}`}
              </TableCell>
              <TableCell align="right">{nf.format(o.ueConnections)}</TableCell>
            </TableRow>
          ))}
          <TableRow>
            <TableCell sx={{ fontWeight: 600 }}>Total</TableCell>
            <TableCell align="right" sx={{ fontWeight: 600 }}>
              {`${nf.format(total.up)} / ${nf.format(total.total)}`}
            </TableCell>
            <TableCell align="right" sx={{ fontWeight: 600 }}>
              {nf.format(total.ueConnections)}
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </TableContainer>
  );
}

export default function Dashboard() {
  const metrics = useQuery({
    queryKey: ["metrics"],
    queryFn: getMetrics,
    refetchInterval: DASHBOARD_REFRESH_MS,
  });

  const operatorList = useQuery({
    queryKey: ["operators"],
    queryFn: listOperators,
    refetchInterval: DASHBOARD_REFRESH_MS,
  });

  const samples = metrics.data ?? [];
  const memory = sumOf(samples, "process_resident_memory_bytes");
  const database = sumOf(samples, "ellamocn_database_storage_bytes");
  const startedAt = sumOf(samples, "process_start_time_seconds");
  const radios = sumOf(samples, "ellamocn_radio_associations", {
    state: "up",
  });
  const ueConnections =
    sumOf(samples, "ellamocn_ue_connections") ??
    (radios === undefined ? undefined : 0);
  const operators = operatorFigures(samples);
  const metricCard = {
    loading: metrics.isPending,
    error: failedToLoad(metrics),
  };
  const count = (n: number | undefined) =>
    n === undefined ? "N/A" : nf.format(n);

  return (
    <Box component="section" aria-labelledby="dashboard-title">
      <PageHeader id="dashboard-title" title="Dashboard" />

      <Section title="Overview" first>
        <Grid size={THIRD}>
          <KpiCard
            title="Operators"
            hint="Operators configured in Ella MOCN."
            to="/operators"
            loading={operatorList.isPending}
            error={failedToLoad(operatorList)}
            value={count(operatorList.data?.length)}
          />
        </Grid>
        <Grid size={THIRD}>
          <KpiCard
            title="Radios"
            hint="Radios that completed S1 Setup or NG Setup with Ella MOCN."
            to="/radios"
            {...metricCard}
            value={count(radios)}
          />
        </Grid>
        <Grid size={THIRD}>
          <KpiCard
            title="UE connections"
            hint="UE-associated logical S1-connections and NG-connections that Ella MOCN relays, across every operator: one per connected UE."
            {...metricCard}
            value={count(ueConnections)}
          />
        </Grid>
        <Grid size={12}>
          <Panel
            title="Core nodes and UE connections"
            to="/operators"
            {...metricCard}
            alignTop
          >
            <OperatorsTable operators={operators} />
          </Panel>
        </Grid>
      </Section>

      <Section title="System">
        <Grid size={THIRD}>
          <KpiCard
            title="Process Memory"
            hint="RAM used by the MOCN process (resident set size): what the operating system and a container memory limit count."
            {...metricCard}
            value={memory === undefined ? "N/A" : formatBytes(memory)}
          />
        </Grid>
        <Grid size={THIRD}>
          <KpiCard
            title="Database Size"
            hint="Size of the database on disk, its write-ahead log included."
            {...metricCard}
            value={database === undefined ? "N/A" : formatBytes(database)}
          />
        </Grid>
        <Grid size={THIRD}>
          <KpiCard
            title="Up Since"
            hint="Time when the MOCN process was started."
            {...metricCard}
            value={
              startedAt === undefined
                ? "N/A"
                : formatDateTime(new Date(startedAt * 1000).toISOString())
            }
          />
        </Grid>
      </Section>
    </Box>
  );
}
