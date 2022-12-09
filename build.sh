set -ex

GOOS=linux GOARCH=amd64 go build -o sqlfmt
BRANCH=$(git symbolic-ref --short HEAD)
SHA=$(git rev-parse --short HEAD)
gcloud --project cockroach-dev-inf builds submit --substitutions=BRANCH_NAME=$BRANCH,SHORT_SHA=$SHA --config cloudbuild.yaml

# kubectl get po | grep sqlfmt | awk '{print $1}' | xargs kubectl delete po
