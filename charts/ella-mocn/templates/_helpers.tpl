{{- define "ella-mocn.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ella-mocn.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "ella-mocn.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ella-mocn.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ella-mocn.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "ella-mocn.labels" -}}
helm.sh/chart: {{ include "ella-mocn.chart" . }}
{{ include "ella-mocn.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/component: mocn
app.kubernetes.io/part-of: ella-networks
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "ella-mocn.headlessServiceName" -}}
{{- printf "%s-headless" (include "ella-mocn.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ella-mocn.ranServiceName" -}}
{{- printf "%s-ran" (include "ella-mocn.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ella-mocn.networkAttachmentName" -}}
{{- default (printf "%s-ran" (include "ella-mocn.fullname" .)) .Values.ran.networkAttachment.name }}
{{- end }}

{{- define "ella-mocn.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "ella-mocn.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "ella-mocn.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag }}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}
{{- end }}

{{- define "ella-mocn.ranIP" -}}
{{- regexReplaceAll "/.*$" .Values.ran.address "" }}
{{- end }}

{{- define "ella-mocn.config" -}}
{{- $ran := dict "signalling" (dict "address" (default "0.0.0.0" (include "ella-mocn.ranIP" .)) "s1_port" 36412 "ng_port" 38412) }}
{{- if .Values.userPlane.enabled }}
{{- $_ := set $ran "user_plane" (dict "enabled" true "address" (include "ella-mocn.ranIP" .)) }}
{{- end }}
{{- $base := dict
  "db" (dict "path" "/data/mocn.db")
  "api" (dict "address" "0.0.0.0" "port" 5030)
  "ran" $ran
}}
{{- toYaml (mustMergeOverwrite $base (deepCopy .Values.config)) }}
{{- end }}
