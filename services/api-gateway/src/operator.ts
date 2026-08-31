import { createHmac, randomUUID, timingSafeEqual } from "crypto";
import { NextFunction, Request, Response } from "express";
import { config } from "./config";

const permissions = ["PAPER_AUTOMATION_PAUSE","PAPER_AUTOMATION_RESUME","PAPER_KILL_SWITCH_ACTIVATE","PAPER_KILL_SWITCH_RELEASE_REQUEST","PAPER_RECONCILIATION_RUN","PAPER_REPAIR_PREVIEW","PAPER_REPAIR_APPLY","PAPER_OPERATIONS_READ"] as const;
type Permission = typeof permissions[number];
type AccessClaims = {iss:string;aud:string;sub:string;iat:number;exp:number;token_use:string;roles:string[]};

const encode = (value: unknown) => Buffer.from(JSON.stringify(value)).toString("base64url");
function verifyAccessToken(raw: string): AccessClaims {
  const match = /^Bearer ([^ ]+)$/.exec(raw.trim());
  if (!match) throw new Error("invalid token");
  const parts = match[1].split(".");
  if (parts.length !== 3) throw new Error("invalid token");
  const header = JSON.parse(Buffer.from(parts[0], "base64url").toString()) as {alg?:string;typ?:string};
  if (header.alg !== "HS256" || header.typ !== "JWT") throw new Error("invalid algorithm");
  const expected = createHmac("sha256", config.JWT_ACCESS_SECRET).update(`${parts[0]}.${parts[1]}`).digest();
  const supplied = Buffer.from(parts[2], "base64url");
  if (expected.length !== supplied.length || !timingSafeEqual(expected, supplied)) throw new Error("invalid signature");
  const claims = JSON.parse(Buffer.from(parts[1], "base64url").toString()) as AccessClaims;
  const now = Math.floor(Date.now()/1000);
  if (claims.iss !== config.JWT_ISSUER || claims.aud !== config.JWT_AUDIENCE || claims.token_use !== "access" || typeof claims.sub !== "string" || !claims.sub || !Array.isArray(claims.roles) || claims.roles.some(role=>typeof role!=="string") || typeof claims.iat!=="number" || typeof claims.exp!=="number" || claims.iat > now + 5 || claims.exp < now || claims.exp<=claims.iat) throw new Error("invalid claims");
  return claims;
}

function rolePermissions(roles: string[]): {role:string;permissions:Permission[]} {
  if (roles.includes("super_admin")) return {role:"security_operator",permissions:[...permissions]};
  if (roles.includes("admin")) return {role:"operator",permissions:permissions.filter(p=>p!=="PAPER_REPAIR_APPLY"&&p!=="PAPER_KILL_SWITCH_RELEASE_REQUEST")};
  throw new Error("forbidden");
}

function assertion(subject:string, role:string, granted:Permission[]):string {
  const now=Math.floor(Date.now()/1000);
  const header=encode({alg:"HS256",typ:"JWT"});
  const payload=encode({ver:"operator-assertion.v1",iss:config.OPERATOR_ASSERTION_ISSUER,aud:config.OPERATOR_ASSERTION_AUDIENCE,sub:subject,role,permissions:granted,iat:now,nbf:now-1,exp:now+config.OPERATOR_ASSERTION_LIFETIME_SECONDS,jti:randomUUID()});
  const signature=createHmac("sha256",config.OPERATOR_ASSERTION_SECRET).update(`${header}.${payload}`).digest("base64url");
  return `${header}.${payload}.${signature}`;
}

export async function operatorProxy(req:Request,res:Response,next:NextFunction):Promise<void>{
  try{
    const claims=verifyAccessToken(req.get("authorization")??"");
    const grant=rolePermissions(claims.roles);
    const operatorPath=req.originalUrl.replace(/^\/api\/v1\/operator/,"");
    const internalPath=operatorPath.startsWith("/runtime")?`/api/v1/paper-automation${operatorPath}`:operatorPath.startsWith("/journal")?`/api/v1/paper${operatorPath}`:`/api/v1/paper-operations${operatorPath}`;
    const target=new URL(internalPath,config.TRADING_ENGINE_URL);
    const upstream=await fetch(target,{method:req.method,headers:{"content-type":"application/json","x-operator-assertion":assertion(claims.sub,grant.role,grant.permissions)},body:req.method==="GET"||req.method==="HEAD"?undefined:JSON.stringify(req.body),signal:AbortSignal.timeout(10000)});
    res.status(upstream.status);upstream.headers.forEach((v,k)=>{if(k.toLowerCase()==="content-type")res.setHeader(k,v)});res.send(await upstream.text());
  }catch(error){
    if(error instanceof Error&&(error.message==="forbidden")){res.status(403).json({error:"forbidden"});return}
    if(error instanceof Error&&error.message.startsWith("invalid")){res.status(401).json({error:"invalid_token"});return}
    next(new Error("operator dependency unavailable"));
  }
}
