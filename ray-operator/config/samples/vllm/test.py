from fastapi import FastAPI
from ray import serve

app = FastAPI()

@serve.deployment(route_prefix="/")
@serve.ingress(app)
class BaseService:
    @app.get("/")
    async def root(self):
        return {"message": "Hello, world!"}

    @app.get("/ping")
    async def ping(self):
        return {"status": "pong"}

# Run with this:
model = BaseService.bind()
