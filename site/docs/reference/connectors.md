### Available connectors

The following Estuary connectors are available. Each connector has a unique configuration you must follow in your [catalog specification](concepts/catalog-entities/README.md); these will be linked below the connector name.

:::warning
Beta note: configurations coming to the docs soon. [Contact the team](mailto:info@estuary.dev) for more information.&#x20;
:::

Links to the package page on GitHub are also provided, including the most recent Docker image.

Estuary is actively developing new connectors, so check back regularly for the latest additions. We’re prioritizing the development of high-scale technological systems, as well as client needs.

#### Capture connectors

* Amazon Kinesis&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/source-kinesis)
* Amazon S3&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/source-s3)
* Apache Kafka&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/source-kafka)
* Google Cloud Storage&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/source-gcs)
* PostgreSQL&#x20;
  * Configuration&#x20;
  * [Package](https://github.com/estuary/connectors/pkgs/container/source-postgres)

#### Materialization connectors

* Apache Parquet&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/materialize-s3-parquet)
* Elasticsearch&#x20;
  * Configuration&#x20;
  * [Package](https://github.com/estuary/connectors/pkgs/container/materialize-elasticsearch)&#x20;
* Google BigQuery&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/materialize-bigquery)
* PostgreSQL&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/materialize-postgres)
* Rockset&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/materialize-rockset)
* Snowflake&#x20;
  * Configuration&#x20;
  * [Package ](https://github.com/estuary/connectors/pkgs/container/materialize-snowflake)
* Webhook&#x20;
  * Configuration&#x20;
  * [Package](https://github.com/estuary/connectors/pkgs/container/materialize-webhook)

#### Third-party connectors

[Many additional connectors are available from Airbyte](https://airbyte.io/connectors). They function similarly but are limited to batch workflows, which Flow will run at a regular and configurable cadence.