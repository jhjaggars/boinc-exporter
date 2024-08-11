FROM registry.fedoraproject.org/fedora-minimal:40

COPY boinc-exporter /boinc-exporter

CMD ["/boinc-exporter"]
