FROM golang:1.12
ADD sqlfmt /
CMD ["/sqlfmt"]
