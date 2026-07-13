# ubiqo Claude Code plugin

Injects your team's shared context (instructions, memories, activity digest)
at session start and reports session activity at session end. Fail-open: if
the ubiqo server is unreachable you get a visible OFFLINE line and the last
cached context — never a blocked session.

Setup (once per machine):

    ubiqo login --server https://ubiqo.example.com --token ubq_...
    cd ~/work/my-project && ubiqo init --project my-project

Install the plugin (from your org's checkout of this repo):

    /plugin marketplace add /path/to/ubiqo
    /plugin install ubiqo@ubiqo

The MCP connector is added separately (see the root README) so the token
never lives inside plugin files.
