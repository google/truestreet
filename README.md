# TrueStreet
A read/write storage endpoint to allow Prometheus to use Google Cloud Spanner
as a timeseries data store. Truestreet leverages Google Cloud Spanner database
to store timeseries data, resulting in a fast and scalable monitoring system.

## Building
Truestreet has migrated from go-dep to go modules to manage versioning and its
dependencies.

To build a truestreet binary from source, first checkout the source code and
then issue go build command inside the truestreet directory.

```shell
git clone https://github.com/google/truestreet.git
```

```shell
cd truestreet
```

```shell
go build
```

```shell
go test
```

### Google Cloud Spanner setup

If you have not already, set up the
[Google Cloud SDK](https://cloud.google.com/sdk/) to use the command line
interface. Make sure to configure the CLI to be on the correct GCP project.

Create an instance with the command

```shell
gcloud spanner instances create truestreet --config=regional-us-central1 --description="TrueStreet Instance" --nodes=1
```

Any of the arguments can be changed to your needs, just make sure to reflect the
name of the instance in TrueStreet's command line arguments. Additionally, it is
recommended to consider how heavy the load will be on Spanner, Kubernetes can
provide automatic scaling of TrueStreet pods, but setting up the appropriate
number of Spanner nodes is not determined by TrueStreet.

### TrueStreet command line arguments

```
port       Port for serving TrueStreet (Default 1760)
project    Name of GCP project
instance   Name of Cloud Spanner instance
db         Name of database inside Cloud Spanner instance
new        Create new database if one does not already exist (Default true)
workers    Number of concurrent routines to write samples (Default 10)
help       Prints help text
```

For example, creating a database called 'truestreet-db' on the Spanner Instance
'truestreet' would require the flags

```
--project myProject --instance truestreet --db truestreet-db
```

By default, TrueStreet wil listen on port `1760`. Using the provided project,
instance and db name, TrueStreet will first attempt to connect to an existing
database on the provided instance. If one is not found and the 'new' flag is set
to true, it will create a database to run on the instance.

## Prometheus Configuration

Add the following lines to your `prometheus.yml` to configure remote read and
write. Additional configuration options are available and can be inspected
[here](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write)
Two suggestions are that, for remote writes, the max samples per second can be
increased from its default of 100 samples. If samples are being written en
masse from Prometheus to TrueStreet, it's more efficient to batch them here as
well, as there are fewer separate requests. TrueStreet batches them to Spanner
anyway. A second suggestion is that, to see TrueStreet reading at work,
Prometheus can be forced to read only from TrueStreet. This is helpful for
testing if the connection is working, and also for retrieving data from other
Prometheus instances that may also be writing to the same Spanner database.

```
remote_write:
    - url: http://<adapter-address>:1760/write
      queue_config:
        max_samples_per_send: 250

remote_read:
    - url: http://<adapter-address>:1760/read
      read_recent: true # Optional to test if read is working
```

TrueStreet also provides Prometheus metrics. These new metrics can be scraped by
adding another target for Prometheus.

```
scrape_configs:
  - job_name: truestreet
    static_configs:
      - targets: ['localhost:1760']
```
