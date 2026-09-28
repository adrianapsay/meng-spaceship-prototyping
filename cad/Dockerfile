FROM python:3.12-slim

# OpenCascade needs a few X/GL libraries even when running headless.
RUN apt-get update \
    && apt-get install -y --no-install-recommends libgl1 libglib2.0-0 libxrender1 \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir "uv>=0.12,<0.13"
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PROJECT_ENVIRONMENT=/opt/venv PATH=/opt/venv/bin:$PATH

WORKDIR /app
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project
COPY src ./src
RUN uv sync --frozen --no-dev

ENV ARTIFACTS_ROOT=/data/artifacts
EXPOSE 8000
CMD ["uvicorn", "cad.app:app", "--host", "0.0.0.0", "--port", "8000"]
