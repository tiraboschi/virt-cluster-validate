# Copyright (C) 2024-2026 Red Hat, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.6

USER 0

# Install Python 3.11 and required tools
RUN microdnf install -y python3.11 jq tar gzip rsync which util-linux-core && \
    microdnf clean all && \
    ln -s /usr/bin/python3.11 /usr/bin/python3

# The validator Job overlays this directory with a writable emptyDir. Keep the
# image PATH directory non-world-writable for standalone execution too.
RUN chown 1001:0 /usr/local/bin

USER 1001

WORKDIR /opt/app-root/src

# Copy the core application files
COPY virt-cluster-validate .
COPY bin/ bin/
COPY checks.d/ checks.d/
COPY README.md .
COPY collection-scripts/gather /usr/bin/gather

# The runner expects 'bin' to be in the PATH
ENV PATH="/opt/app-root/src/bin:${PATH}"

# ENTRYPOINT is gather which detects must-gather vs normal mode
ENTRYPOINT ["/usr/bin/gather"]
