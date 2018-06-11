set -ex

IMG=gcr.io/hots-cockroach/sqlfmt:latest

go build -o sqlfmt
docker build -t $IMG .
docker push $IMG
kubectl get po | grep sqlfmt | awk '{print $1}' | xargs kubectl delete po
