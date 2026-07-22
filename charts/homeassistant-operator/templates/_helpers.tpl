{{/*
Expand the name of the chart.
*/}}
{{- define "homeassistant-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "homeassistant-operator.fullname" -}}
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
Create chart label.
*/}}
{{- define "homeassistant-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "homeassistant-operator.labels" -}}
helm.sh/chart: {{ include "homeassistant-operator.chart" . }}
{{ include "homeassistant-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "homeassistant-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "homeassistant-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "homeassistant-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "homeassistant-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Operator image reference.
*/}}
{{- define "homeassistant-operator.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
Webhook resource names.

.fullname is already truncated to 63 chars (the Kubernetes Service DNS-label
limit), but appending a suffix afterwards can push the result back over that
limit for long release names/namespaces. Truncate again after concatenation,
and share these definitions between webhook.yaml and deployment.yaml so the
operator always looks up the exact same names Helm actually rendered.
*/}}
{{- define "homeassistant-operator.webhookServiceName" -}}
{{- printf "%s-webhook-service" (include "homeassistant-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "homeassistant-operator.webhookCertSecretName" -}}
{{- printf "%s-webhook-server-cert" (include "homeassistant-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "homeassistant-operator.webhookCertificateName" -}}
{{- printf "%s-webhook-serving-cert" (include "homeassistant-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "homeassistant-operator.validatingWebhookConfigurationName" -}}
{{- printf "%s-validating-webhook-configuration" (include "homeassistant-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
