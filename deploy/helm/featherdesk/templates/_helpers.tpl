{{/* SPDX-License-Identifier: AGPL-3.0-only */}}
{{- define "featherdesk.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "featherdesk.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "featherdesk.labels" -}}
{{ include "featherdesk.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "featherdesk.secretName" -}}
{{- if .Values.secret.existingSecret -}}
{{- .Values.secret.existingSecret -}}
{{- else -}}
{{- include "featherdesk.fullname" . -}}
{{- end -}}
{{- end -}}

{{/* Guard rails evaluated by every template that includes it. */}}
{{- define "featherdesk.validate" -}}
{{- if ne (int .Values.replicaCount) 1 -}}
{{- fail (printf "replicaCount must be 1 (got %v): FeatherDesk runs its queue, WebSocket hub and write-behind in-process and locks /data; two pods would corrupt state" .Values.replicaCount) -}}
{{- end -}}
{{- if and (not .Values.secret.existingSecret) (not .Values.secret.create) -}}
{{- fail "set secret.existingSecret (a Secret containing FD_SECRET_KEY) or secret.create=true with secret.values.FD_SECRET_KEY" -}}
{{- end -}}
{{- if and .Values.blob.url (not .Values.blob.existingSecret) -}}
{{- fail "blob.url (S3) requires blob.existingSecret with FD_S3_ACCESS_KEY and FD_S3_SECRET_KEY" -}}
{{- end -}}
{{- end -}}
