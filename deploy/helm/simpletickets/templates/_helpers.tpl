{{/* SPDX-License-Identifier: AGPL-3.0-only */}}
{{- define "simpletickets.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "simpletickets.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "simpletickets.labels" -}}
{{ include "simpletickets.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "simpletickets.secretName" -}}
{{- if .Values.secret.existingSecret -}}
{{- .Values.secret.existingSecret -}}
{{- else -}}
{{- include "simpletickets.fullname" . -}}
{{- end -}}
{{- end -}}

{{/* Guard rails evaluated by every template that includes it. */}}
{{- define "simpletickets.validate" -}}
{{- if ne (int .Values.replicaCount) 1 -}}
{{- fail (printf "replicaCount must be 1 (got %v): SimpleTickets runs its queue, WebSocket hub and write-behind in-process and locks /data; two pods would corrupt state" .Values.replicaCount) -}}
{{- end -}}
{{- if and (not .Values.secret.existingSecret) (not .Values.secret.create) -}}
{{- fail "set secret.existingSecret (a Secret containing ST_SECRET_KEY) or secret.create=true with secret.values.ST_SECRET_KEY" -}}
{{- end -}}
{{- if and .Values.blob.url (not .Values.blob.existingSecret) -}}
{{- fail "blob.url (S3) requires blob.existingSecret with ST_S3_ACCESS_KEY and ST_S3_SECRET_KEY" -}}
{{- end -}}
{{- end -}}
