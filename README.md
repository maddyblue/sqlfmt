# sqlfmt
Experimental SQL formatter with width-aware output

Based on http://homepages.inf.ed.ac.uk/wadler/papers/prettier/prettier.pdf.

Requires having the master branch of https://github.com/cockroachdb/cockroach checked out at `$GOPATH/src/github.com/cockroachdb/cockroach`, and running `make` in that directory.

# Deployment

To deploy:

1. Ensure your kubectl creds for the desired cluster are present.
   * See https://github.com/cockroachlabs/h2hello 
2. Run `./build.sh`
3. If everything went well, kill current pods so they will be redeployed from the new docker image:
`kubectl get po | grep sqlfmt | awk '{print $1}' | xargs kubectl delete pod`
   * Note that this will work only if your branch is the same branch used by
     the service. You can use the below command to set the image:
   * `kubectl set image deployment/sqlfmt sqlfmt=gcr.io/cockroach-dev-inf/cockroachlabs/sqlfmt:master`

Note that step 8 will only work if you're on the master branch.
