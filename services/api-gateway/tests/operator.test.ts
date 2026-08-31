import { createHmac } from "crypto";
import request from "supertest";
import { app } from "../src/app";

const secret = "test-only-operator-security-secret-0123456789abcdef-0123456789abcdef";
const encode = (v:unknown)=>Buffer.from(JSON.stringify(v)).toString("base64url");
function accessToken(roles:string[],changes:Record<string,unknown>={}):string{
  const now=Math.floor(Date.now()/1000);const h=encode({alg:"HS256",typ:"JWT"});const p=encode({iss:"compound-trader",aud:"compound-trader-platform",sub:"operator-1",iat:now,exp:now+60,token_use:"access",roles,...changes});const s=createHmac("sha256",secret).update(`${h}.${p}`).digest("base64url");return `${h}.${p}.${s}`;
}

describe("operator trust boundary",()=>{
  afterEach(()=>jest.restoreAllMocks());
  it("rejects missing, invalid and unknown-role access tokens",async()=>{
    expect((await request(app).post("/api/v1/operator/pause").send({})).status).toBe(401);
    expect((await request(app).post("/api/v1/operator/pause").set("authorization","Bearer bad").send({})).status).toBe(401);
    expect((await request(app).post("/api/v1/operator/pause").set("authorization",`Bearer ${accessToken(["user"])}`).send({})).status).toBe(403);
  });
  it("mints a bounded assertion and never forwards client identity headers",async()=>{
    const fetchMock=jest.spyOn(global,"fetch").mockResolvedValue(new Response(JSON.stringify({outcome:"ACCEPTED"}),{status:200,headers:{"content-type":"application/json"}}));
    const response=await request(app).post("/api/v1/operator/pause").set("authorization",`Bearer ${accessToken(["admin"])}`).set("x-operator-assertion","attacker").send({requestId:"r"});
    expect(response.status).toBe(200);const init=fetchMock.mock.calls[0][1] as RequestInit;const headers=init.headers as Record<string,string>;expect(headers["x-operator-assertion"]).not.toBe("attacker");const parts=headers["x-operator-assertion"].split(".");const claims=JSON.parse(Buffer.from(parts[1],"base64url").toString());expect(claims).toMatchObject({ver:"operator-assertion.v1",iss:"compound-api-gateway",aud:"compound-trading-engine",sub:"operator-1",role:"operator"});expect(claims.permissions).not.toContain("PAPER_REPAIR_APPLY");expect(claims.exp-claims.iat).toBeLessThanOrEqual(60);
  });
  it("reserves strongest permissions for super_admin",async()=>{
    const fetchMock=jest.spyOn(global,"fetch").mockResolvedValue(new Response("{}",{status:200,headers:{"content-type":"application/json"}}));await request(app).post("/api/v1/operator/repairs/apply").set("authorization",`Bearer ${accessToken(["super_admin"])}`).send({});const init=fetchMock.mock.calls[0][1] as RequestInit;const token=(init.headers as Record<string,string>)["x-operator-assertion"];const claims=JSON.parse(Buffer.from(token.split(".")[1],"base64url").toString());expect(claims.role).toBe("security_operator");expect(claims.permissions).toContain("PAPER_REPAIR_APPLY");expect(claims.permissions).not.toContain("LIVE");
  });
});
