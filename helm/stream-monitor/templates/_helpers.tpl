{{/*
Expand the name of the chart.
*/}}
{{- define "stream-monitor.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "stream-monitor.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "stream-monitor.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "stream-monitor.labels" -}}
helm.sh/chart: {{ include "stream-monitor.chart" . }}
{{ include "stream-monitor.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "stream-monitor.selectorLabels" -}}
app.kubernetes.io/name: {{ include "stream-monitor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Gateway labels
*/}}
{{- define "stream-monitor.gateway.labels" -}}
{{ include "stream-monitor.labels" . }}
app.kubernetes.io/component: gateway
{{- end }}

{{/*
Gateway selector labels
*/}}
{{- define "stream-monitor.gateway.selectorLabels" -}}
{{ include "stream-monitor.selectorLabels" . }}
app.kubernetes.io/component: gateway
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "stream-monitor.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "stream-monitor.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the database URL
*/}}
{{- define "stream-monitor.databaseURL" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "postgres://%s:$(DATABASE_PASSWORD)@%s-postgresql:%d/%s?sslmode=disable" .Values.postgresql.username (include "stream-monitor.fullname" .) (int .Values.postgresql.port) .Values.postgresql.database }}
{{- else }}
{{- printf "postgres://%s:$(DATABASE_PASSWORD)@%s:%d/%s?sslmode=disable" .Values.postgresql.username .Values.postgresql.host (int .Values.postgresql.port) .Values.postgresql.database }}
{{- end }}
{{- end }}
