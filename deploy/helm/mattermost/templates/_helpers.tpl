{{/*
Expand the name of the chart.
*/}}
{{- define "mattermost.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "mattermost.fullname" -}}
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
{{- define "mattermost.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "mattermost.labels" -}}
helm.sh/chart: {{ include "mattermost.chart" . }}
{{ include "mattermost.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "mattermost.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mattermost.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "mattermost.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "mattermost.fullname" .) .Values.serviceAccount.name }}
{{- else if .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name }}
{{- else }}
{{- printf "%s-ha" .Release.Name }}
{{- end }}
{{- end }}

{{/*
PostgreSQL connection string
*/}}
{{- define "mattermost.postgresql.datasource" -}}
{{- if .Values.postgresql.enabled }}
postgres://{{ .Values.postgresql.auth.username }}:{{ .Values.postgresql.auth.password }}@{{ include "mattermost.fullname" . }}-postgres:5432/{{ .Values.postgresql.auth.database }}?sslmode=disable&connect_timeout=10
{{- else }}
{{- .Values.mattermost.config.SqlSettings.DataSource }}
{{- end }}
{{- end }}

{{/*
Gossip peer addresses as JSON array string
*/}}
{{- define "mattermost.gossip.peers.json" -}}
{{- $replicas := .Values.replicaCount | int }}
{{- $name := include "mattermost.fullname" . }}
{{- $port := .Values.mattermost.config.ClusterSettings.GossipPort }}
{{- $peers := list }}
{{- range $i := until $replicas }}
  {{- $peer := printf "%s-%d.%s-headless:%d" $name $i $name $port }}
  {{- $peers = append $peers $peer }}
{{- end }}
{{- $peers | toJson | quote }}
{{- end }}

{{/*
Get lease namespace from mattermost-ha subchart or use default
*/}}
{{- define "mattermost.lease.namespace" -}}
{{- $namespace := "" }}
{{- if and .Values.mattermost-ha .Values.mattermost-ha.cluster .Values.mattermost-ha.cluster.lease }}
  {{- if .Values.mattermost-ha.cluster.lease.namespace }}
    {{- $namespace = .Values.mattermost-ha.cluster.lease.namespace }}
  {{- end }}
{{- end }}
{{- if not $namespace }}
  {{- if .Values.mattermost.config.ClusterSettings.LeaderElectionK8sNamespace }}
    {{- $namespace = .Values.mattermost.config.ClusterSettings.LeaderElectionK8sNamespace }}
  {{- end }}
{{- end }}
{{- if not $namespace }}
{{- $namespace = .Release.Namespace }}
{{- end }}
{{- $namespace }}
{{- end }}

{{/*
Get lease name from mattermost-ha subchart or use default
*/}}
{{- define "mattermost.lease.name" -}}
{{- if and .Values.mattermost-ha .Values.mattermost-ha.cluster .Values.mattermost-ha.cluster.lease .Values.mattermost-ha.cluster.lease.name }}
{{- .Values.mattermost-ha.cluster.lease.name }}
{{- else if .Values.mattermost.config.ClusterSettings.LeaderElectionK8sLeaseName }}
{{- .Values.mattermost.config.ClusterSettings.LeaderElectionK8sLeaseName }}
{{- else }}
{{- printf "%s-ha" .Release.Name }}
{{- end }}
{{- end }}

