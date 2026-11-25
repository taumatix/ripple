package model

// DatasourceHelm is the versions of a chart in a Helm chart repository (its index.yaml): the
// dependencies of a Chart.yaml or requirements.yaml, named "<repository URL>/<chart>" (a chart
// name has no slash, so the last one splits them).
const DatasourceHelm = "helm"
