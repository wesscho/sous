# What is Sous?

Sous is a tool for building, testing, and deploying software in the cloud.

Sous consists of three major components:

- **Sous Engine** (a library) provides the main functionality of Sous.
- **Sous Server** provides functionality and data using Sous Core.
- **Sous CLI** provides the same tools as Sous Server, on the command line, as
  well as additional tools for local development.

All of these components are data-driven, and do not make any assumptions
about organisation-specific factors. To use sous, you need to provide configuration
describing your organisation's infrastructure, applications, and policies:

- **Infrastructure Configuration** describes your organisation's datacentres
 and environments.
- **Application Configuration** describes your organisation's applications.
- **Policy Configration** describes, in code, your organisation's policies about
  what can and cannot be deployed where. This uses the concept of "contracts"
  which are automated integration tests you expect deployable artefacts to pass
  before they can be deployed.
- **Sous Buildpacks** provide standardised build pipelines for your applications.

Using this configuration, Sous allows users to create deployments of applications
to infrastructure. This organisation-wide data structure is called "Sous State".
Sous State is a version-controlled artefact declaring all of your organisation's
deployed applications, and their version, and their environmental configuration.
Sous State is generated by Sous Engine based on events.

Sous State contains the following information:

- **Global Deployment Manifest** (GDM) is the main data structure, containing the
  entire global deployment state.
- **GDM History** is the entire history of all previous GDMs, allowing reflection
  and auditing of changes.

## Sous Engine

TODO 

## Sous Server

TODO

## Sous CLI

TODO

## Sous Configuration

TODO

### Infrastructure

TODO

### Applications

TODO

### Policies

TODO

### Buildpacks

TODO

## Sous State

TODO

### Global Deployment Manifest

TODO

### GDM History

TODO

