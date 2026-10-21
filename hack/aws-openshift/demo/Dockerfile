# The demo image: the narrated front end and the embedding job, with the
# embedding model and the corpus (the Kubernetes documentation) baked in.
FROM registry.access.redhat.com/ubi9/python-312:latest

USER 0
COPY requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt

ENV FASTEMBED_CACHE_PATH=/opt/models \
    MODEL=BAAI/bge-base-en-v1.5 \
    PYTHONPATH=/opt/app \
    CORPUS=/opt/corpus/k8s-docs.jsonl.gz
RUN python -c "import os; from fastembed import TextEmbedding; TextEmbedding(os.environ['MODEL'], cache_dir=os.environ['FASTEMBED_CACHE_PATH'])"

COPY caplv_demo /opt/app/caplv_demo
RUN git clone --quiet --depth 1 --filter=blob:none --sparse https://github.com/kubernetes/website /tmp/website && \
    git -C /tmp/website sparse-checkout set content/en/docs && \
    mkdir -p /opt/corpus && \
    python -m caplv_demo.corpus /tmp/website/content/en/docs "$CORPUS" && \
    rm -rf /tmp/website && \
    chgrp -R 0 /opt/models /opt/app /opt/corpus && chmod -R g=u /opt/models /opt/app /opt/corpus

# The model is baked in above: never reach out for it at run time, and give
# OpenShift's arbitrary UID a writable home.
ENV HF_HUB_OFFLINE=1 \
    HOME=/tmp
WORKDIR /opt/app
USER 1001
EXPOSE 8080
CMD ["uvicorn", "caplv_demo.app:app", "--host", "0.0.0.0", "--port", "8080", "--log-level", "warning"]
