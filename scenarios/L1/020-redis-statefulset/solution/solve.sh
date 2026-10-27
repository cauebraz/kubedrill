#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}" "${VAR_POLICY:?VAR_POLICY is required}" "${VAR_CONFIG:?VAR_CONFIG is required}"

image=redis:7.4-alpine@sha256:6ba19c50b56777f5c11c58eff8ddb69d0d5f27ba131cffb63f180ccfd43c66af

args=""
mounts=""
volumes=""
if [ "$VAR_CONFIG" != none ]; then
  args='          args: ["redis-server", "/usr/local/etc/redis/redis.conf"]'
  mounts='            - name: config
              mountPath: /usr/local/etc/redis/redis.conf
              subPath: redis.conf
              readOnly: true'
  volumes="      volumes:
        - name: config
          configMap:
            name: $VAR_CONFIG"
fi

kubectl -n "$TASK_NS" apply -f - <<YAML
apiVersion: v1
kind: Service
metadata:
  name: redis
  namespace: $TASK_NS
  labels:
    app: redis
spec:
  clusterIP: None
  selector:
    app: redis
  ports:
    - name: redis
      port: 6379
      targetPort: redis
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis
  namespace: $TASK_NS
  labels:
    app: redis
spec:
  serviceName: redis
  replicas: 3
  podManagementPolicy: $VAR_POLICY
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      terminationGracePeriodSeconds: 1
      containers:
        - name: redis
          image: $image
$args
          ports:
            - name: redis
              containerPort: 6379
          readinessProbe:
            exec:
              command: ["redis-cli", "ping"]
            periodSeconds: 2
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              memory: 96Mi
          volumeMounts:
            - name: data
              mountPath: /data
$mounts
$volumes
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: ["ReadWriteOnce"]
        storageClassName: standard
        resources:
          requests:
            storage: 100Mi
YAML

kubectl -n "$TASK_NS" rollout status statefulset/redis --timeout=240s
