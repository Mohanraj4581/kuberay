from fastapi import FastAPI
from ray import serve
import ray

# Start Ray and Serve
ray.init()
serve.start(detached=True)

# Create a FastAPI app
app = FastAPI()

@app.get("/")
def root():
    return {"message": "Ray Serve with FastAPI is working!"}

@app.get("/square/{number}")
def square(number: int):
    return {"input": number, "output": number * number}

# Ray Serve Deployment
@serve.deployment(route_prefix="/")
@serve.ingress(app)
class FastAPIWrapper:
    pass

# Deploy the app
model = FastAPIWrapper.deploy()
