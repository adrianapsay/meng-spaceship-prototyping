FROM python:3.12-slim

RUN pip install --no-cache-dir "uv>=0.12,<0.13"
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PROJECT_ENVIRONMENT=/opt/venv PATH=/opt/venv/bin:$PATH

WORKDIR /app
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project
COPY src ./src
RUN uv sync --frozen --no-dev

EXPOSE 8001
CMD ["uvicorn", "agent.app:app", "--host", "0.0.0.0", "--port", "8001"]
