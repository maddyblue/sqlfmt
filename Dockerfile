FROM ubuntu:xenial-20170915
RUN apt-get update && apt-get install -y ca-certificates && apt-get clean
ADD sqlfmt /
CMD ["/sqlfmt"]
